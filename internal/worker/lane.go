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
	"github.com/ihorbryk/seo-site-checker/internal/exclude"
	"github.com/ihorbryk/seo-site-checker/internal/links"
	"github.com/ihorbryk/seo-site-checker/internal/seo"
	"github.com/ihorbryk/seo-site-checker/internal/urlnorm"
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
	lanes := chk.Lanes
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
		lanes--
	} else if raised, err := raiseLanes(ctx, rc, tx, q, chk, job.Args); err != nil {
		return err
	} else {
		lanes += raised
	}
	if lanes != chk.Lanes {
		if err := q.SetCheckLanes(ctx, dbgen.SetCheckLanesParams{ID: chk.ID, Lanes: lanes}); err != nil {
			return err
		}
	}
	if !continued && lanes == 0 {
		if _, err := rc.InsertTx(ctx, tx, check.FinalizeCheckArgs{CheckID: chk.ID}, nil); err != nil {
			return err
		}
	}

	if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// raiseLanes starts extra lanes, each with a Fetch already claimed, while
// Fetches are pending and the Check runs fewer than max_parallel lanes (for
// example after discovery added Fetches). It runs under the Check row lock and
// returns how many lanes it started.
func raiseLanes(ctx context.Context, rc *river.Client[pgx.Tx], tx pgx.Tx, q *dbgen.Queries, chk dbgen.Check, args check.LaneArgs) (int32, error) {
	settings, err := check.ParseSettings(chk.Settings)
	if err != nil {
		return 0, err
	}
	pending, err := q.CountPendingFetches(ctx, chk.ID)
	if err != nil {
		return 0, err
	}
	extra := min(int64(settings.MaxParallel)-int64(chk.Lanes), pending)
	var started int32
	for ; extra > 0; extra-- {
		lane, err := rc.InsertTx(ctx, tx, args, nil)
		if err != nil {
			return 0, err
		}
		if _, err := q.ClaimNextFetch(ctx, dbgen.ClaimNextFetchParams{
			JobID: pgtype.Int8{Int64: lane.Job.ID, Valid: true}, CheckID: chk.ID,
		}); err != nil {
			return 0, err
		}
		started++
	}
	return started, nil
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
	if err := q.FinishFetchSnapshot(ctx, dbgen.FinishFetchSnapshotParams{ID: f.ID, DurationMs: dur}); err != nil {
		return err
	}
	if res.HasBody && res.Status >= 200 && res.Status < 300 {
		return discover(ctx, q, chk, f, res.Body)
	}
	return nil
}

// discover stores the outgoing links of a 2xx HTML Snapshot, turns new URLs
// into discovered Pages with pending Fetches, and records dropped URLs.
func discover(ctx context.Context, q *dbgen.Queries, chk dbgen.Check, f *dbgen.GetClaimedFetchRow, body []byte) error {
	found, err := links.Extract(body, f.Url, urlnorm.Host(f.Url))
	if err != nil {
		return err
	}
	// Excluded URLs (by the Check's copy of the patterns) are not added to the
	// pool and get no Fetch; the seed Page can't be excluded.
	settings, err := check.ParseSettings(chk.Settings)
	if err != nil {
		return err
	}
	seed := "https://" + urlnorm.Host(f.Url) + "/"
	kept := found.Links[:0:0]
	for _, l := range found.Links {
		if l.URL == seed || !exclude.Excluded(settings.ExcludePatterns, l.URL) {
			kept = append(kept, l)
		}
	}
	found.Links = kept
	if len(found.Links) > 0 {
		urls := make([]string, 0, len(found.Links))
		for _, l := range found.Links {
			urls = append(urls, l.URL)
		}
		if _, err := q.UpsertDiscoveredPages(ctx, dbgen.UpsertDiscoveredPagesParams{ProjectID: f.ProjectID, Urls: urls}); err != nil {
			return err
		}
		pages, err := q.PageIDsByURL(ctx, dbgen.PageIDsByURLParams{ProjectID: f.ProjectID, Urls: urls})
		if err != nil {
			return err
		}
		idOf := make(map[string]int64, len(pages))
		ids := make([]int64, 0, len(pages))
		for _, p := range pages {
			idOf[p.Url] = p.ID
			ids = append(ids, p.ID)
		}
		if _, err := q.PlanDiscoveredFetches(ctx, dbgen.PlanDiscoveredFetchesParams{CheckID: chk.ID, PageIds: ids}); err != nil {
			return err
		}
		bySource := map[string][]int64{}
		for _, l := range found.Links {
			bySource[l.Source] = append(bySource[l.Source], idOf[l.URL])
		}
		for source, pageIDs := range bySource {
			err := q.InsertLinks(ctx, dbgen.InsertLinksParams{SnapshotID: f.ID, Source: source, PageIds: pageIDs})
			if err != nil {
				return err
			}
		}
	}
	byReason := map[string][]string{}
	for _, d := range found.Dropped {
		byReason[d.Reason] = append(byReason[d.Reason], d.URL)
	}
	for reason, urls := range byReason {
		if err := q.InsertDroppedURLs(ctx, dbgen.InsertDroppedURLsParams{CheckID: chk.ID, Reason: reason, Urls: urls}); err != nil {
			return err
		}
	}
	return nil
}
