package worker

import (
	"context"
	"errors"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type finalizeWorker struct {
	river.WorkerDefaults[check.FinalizeCheckArgs]
	*deps
}

// Work records which Pages were orphaned, vanished or retired, caches the Report counts and marks the Check finished. Repeating it on
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
	if err := countOrphans(ctx, q, c.ID); err != nil {
		return err
	}
	if err := q.FinishCheck(ctx, c.ID); err != nil {
		return err
	}
	if err := q.SetCheckEventCounts(ctx, c.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// countOrphans updates the orphan streaks and records vanished and retired
// events. A Check whose seed Page did not end in a Snapshot saw no links at
// all, so it skips counting (and the Report says so).
func countOrphans(ctx context.Context, q *dbgen.Queries, checkID int64) error {
	outcome, err := q.SeedFetchOutcome(ctx, checkID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err != nil || outcome.String != "snapshot" {
		return q.MarkOrphanCountingSkipped(ctx, checkID)
	}
	if err := q.ResetLinkedOrphanStreaks(ctx, checkID); err != nil {
		return err
	}
	orphans, err := q.CountOrphans(ctx, checkID)
	if err != nil {
		return err
	}
	var vanished, retired []int64
	for _, o := range orphans {
		if o.OrphanStreak == 1 {
			vanished = append(vanished, o.ID)
		}
		if o.OrphanStreak >= RetireAfter {
			retired = append(retired, o.ID)
		}
	}
	for kind, ids := range map[string][]int64{"vanished": vanished, "retired": retired} {
		if len(ids) == 0 {
			continue
		}
		if err := q.InsertPageEvents(ctx, dbgen.InsertPageEventsParams{CheckID: checkID, Kind: kind, PageIds: ids}); err != nil {
			return err
		}
	}
	return nil
}

// RetireAfter is how many consecutive orphaned Checks retire a Page. It
// mirrors the constant in the CountOrphans query.
const RetireAfter = 3
