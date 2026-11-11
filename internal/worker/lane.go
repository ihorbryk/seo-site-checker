package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/seo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type laneWorker struct {
	river.WorkerDefaults[check.LaneArgs]
	*deps
}

func (w *laneWorker) Timeout(job *river.Job[check.LaneArgs]) time.Duration {
	return laneTimeout(job.Args.FetchTimeoutS)
}

// Work performs the Fetch claimed under this job's id and then, in one
// transaction, stores the result, claims the next Fetch, re-enqueues the lane
// and completes this job. Transport errors are Fetch failures, never job
// errors. Only our own errors (database errors, panics, the worker stopping)
// are job errors. A retried lane finds its Fetch still claimed and runs it
// again from scratch; on the last attempt the Fetch is recorded as an
// `internal` failure and the lane carries on, so the Check still finishes.
func (w *laneWorker) Work(ctx context.Context, job *river.Job[check.LaneArgs]) error {
	claimed, err := w.q.GetClaimedFetch(ctx, pgtype.Int8{Int64: job.ID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return w.advance(ctx, job, nil, fetchResult{})
	}
	if err != nil {
		return w.lastAttempt(ctx, job, nil, err)
	}
	return w.lastAttempt(ctx, job, &claimed, w.perform(ctx, job, claimed))
}

// perform runs the claimed Fetch and stores its result. A panic is returned as
// an error.
func (w *laneWorker) perform(ctx context.Context, job *river.Job[check.LaneArgs], claimed dbgen.GetClaimedFetchRow) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	chk, err := w.q.GetCheck(ctx, claimed.CheckID)
	if err != nil {
		return err
	}
	settings, err := check.ParseSettings(chk.Settings)
	if err != nil {
		return err
	}
	res, err := fetch(ctx, w.client, claimed.Url, settings)
	if err != nil {
		return err // the worker is stopping: let River retry the lane
	}
	return w.advance(ctx, job, &claimed, res)
}

// lastAttempt returns err so River retries the lane, except on the last
// attempt, where retrying is over: the claimed Fetch (if any) is recorded as
// an `internal` failure and the lane advances anyway.
func (w *laneWorker) lastAttempt(ctx context.Context, job *river.Job[check.LaneArgs], claimed *dbgen.GetClaimedFetchRow, err error) error {
	if err == nil || job.Attempt < job.MaxAttempts {
		return err
	}
	w.log.Error("lane failed on its last attempt; recording an internal failure", "job", job.ID, "err", err)
	// The job's context may be what ended (timeout, shutdown); this must still land.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	failed := fetchResult{FailKind: FailInternal, FailMsg: err.Error()}
	return w.advance(ctx, job, claimed, failed)
}

// advance is the lane's single transaction. claimed is nil when the lane has
// no Fetch to store (it only ends).
func (w *laneWorker) advance(ctx context.Context, job *river.Job[check.LaneArgs], claimed *dbgen.GetClaimedFetchRow, res fetchResult) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := w.q.WithTx(tx)

	// The Check row lock serialises every change to `lanes`.
	chk, err := q.LockCheck(ctx, job.Args.CheckID)
	if err != nil {
		return err
	}
	if claimed != nil {
		if err := storeResult(ctx, q, chk, claimed, res); err != nil {
			return err
		}
	}

	rc := river.ClientFromContext[pgx.Tx](ctx)
	continued := false
	if chk.State == check.StateRunning {
		next, err := q.ClaimNextFetch(ctx, dbgen.ClaimNextFetchParams{
			JobID: pgtype.Int8{Int64: job.ID, Valid: true}, CheckID: chk.ID,
		})
		switch {
		case err == nil:
			// The next lane job's id is known only once it is inserted.
			lane, err := rc.InsertTx(ctx, tx, job.Args, nil)
			if err != nil {
				return err
			}
			err = q.SetFetchJob(ctx, dbgen.SetFetchJobParams{ID: next, JobID: pgtype.Int8{Int64: lane.Job.ID, Valid: true}})
			if err != nil {
				return err
			}
			continued = true
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return err
		}
	}
	if !continued {
		lanes := chk.Lanes - 1
		if err := q.SetCheckLanes(ctx, dbgen.SetCheckLanesParams{ID: chk.ID, Lanes: lanes}); err != nil {
			return err
		}
		if lanes == 0 {
			if _, err := rc.InsertTx(ctx, tx, check.FinalizeCheckArgs{CheckID: chk.ID}, nil); err != nil {
				return err
			}
		}
	}

	if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func storeResult(ctx context.Context, q *dbgen.Queries, chk dbgen.Check, f *dbgen.GetClaimedFetchRow, res fetchResult) error {
	dur := pgtype.Int4{Int32: int32(res.Duration.Milliseconds()), Valid: true}
	if res.FailKind != "" {
		return q.FinishFetchFailure(ctx, dbgen.FinishFetchFailureParams{
			ID:          f.ID,
			FailureKind: pgtype.Text{String: res.FailKind, Valid: true},
			Message:     pgtype.Text{String: res.FailMsg, Valid: true},
			DurationMs:  dur,
		})
	}
	var sum []byte
	if res.HasBody {
		h := sha256.Sum256(res.Body)
		sum = h[:]
		err := q.InsertBody(ctx, dbgen.InsertBodyParams{
			ProjectID: f.ProjectID, Sha256: sum, Content: res.Body, Size: int32(len(res.Body)),
		})
		if err != nil {
			return err
		}
	}
	headers, err := json.Marshal(res.Headers)
	if err != nil {
		return err
	}
	seoFields, err := json.Marshal(seo.Extract(seo.Input{
		Status: res.Status, Headers: res.Headers, Body: res.Body, HasBody: res.HasBody,
	}))
	if err != nil {
		return err
	}
	err = q.InsertSnapshot(ctx, dbgen.InsertSnapshotParams{
		ID: f.ID, PageID: f.PageID, CheckID: chk.ID, FetchedAt: time.Now(),
		Status: int32(res.Status), Headers: headers, BodySha256: sum, SeoFields: seoFields,
	})
	if err != nil {
		return err
	}
	return q.FinishFetchSnapshot(ctx, dbgen.FinishFetchSnapshotParams{ID: f.ID, DurationMs: dur})
}
