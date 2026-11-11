package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type startWorker struct {
	river.WorkerDefaults[check.StartCheckArgs]
	*deps
}

// Work copies the Request settings onto the Check, marks it running, plans one
// Fetch per active Page and starts up to max_parallel lanes, each with a Fetch
// already claimed under its job id. It is safe to repeat: a Check that is no
// longer queued is left alone.
func (w *startWorker) Work(ctx context.Context, job *river.Job[check.StartCheckArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := w.q.WithTx(tx)

	c, err := q.LockCheck(ctx, job.Args.CheckID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the Check is gone; nothing to start
		}
		return err
	}
	if c.State != check.StateQueued {
		return nil
	}
	project, err := q.GetProject(ctx, c.ProjectID)
	if err != nil {
		return err
	}
	settings, err := check.SettingsOf(project)
	if err != nil {
		return err
	}
	rawSettings, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	pending, err := q.PlanFetches(ctx, dbgen.PlanFetchesParams{CheckID: c.ID, ProjectID: c.ProjectID})
	if err != nil {
		return err
	}
	lanes := min(int64(settings.MaxParallel), pending)

	rc := river.ClientFromContext[pgx.Tx](ctx)
	for range lanes {
		laneJob, err := rc.InsertTx(ctx, tx, check.LaneArgs{CheckID: c.ID, FetchTimeoutS: settings.TimeoutS}, nil)
		if err != nil {
			return err
		}
		if _, err := q.ClaimNextFetch(ctx, dbgen.ClaimNextFetchParams{
			JobID: pgtype.Int8{Int64: laneJob.Job.ID, Valid: true}, CheckID: c.ID,
		}); err != nil {
			return err
		}
	}
	if lanes == 0 { // nothing to fetch: finish straight away
		if _, err := rc.InsertTx(ctx, tx, check.FinalizeCheckArgs{CheckID: c.ID}, nil); err != nil {
			return err
		}
	}
	err = q.MarkCheckRunning(ctx, dbgen.MarkCheckRunningParams{
		ID: c.ID, Settings: rawSettings, NoiseRules: []byte("[]"), Lanes: int32(lanes),
	})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
