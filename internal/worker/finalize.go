package worker

import (
	"context"
	"errors"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type finalizeWorker struct {
	river.WorkerDefaults[check.FinalizeCheckArgs]
	*deps
}

// Work caches the Report counts and marks the Check finished. Repeating it on
// a Check that has ended does nothing.
func (w *finalizeWorker) Work(ctx context.Context, job *river.Job[check.FinalizeCheckArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := w.q.WithTx(tx)

	c, err := q.LockCheck(ctx, job.Args.CheckID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if c.State != check.StateRunning {
		return nil
	}
	if err := q.FinishCheck(ctx, c.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
