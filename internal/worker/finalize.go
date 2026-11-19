package worker

import (
	"cmp"
	"context"
	"errors"
	"math"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// DefaultFinalizeBatchSize is how many of a Check's Fetches bound one
// FinalizeCheck job's batch of Pages (ADR 0007): under 0.8 s on a laptop on a
// 2M-Page Check, far below the job timeout however large the Check.
const DefaultFinalizeBatchSize = 10_000

// finalizeTimeout is a FinalizeCheck job's River timeout. A batch takes well
// under it; it must stay below rescueStuckJobsAfter.
const finalizeTimeout = 3 * time.Minute

type finalizeWorker struct {
	river.WorkerDefaults[check.FinalizeCheckArgs]
	*deps
}

func (w *finalizeWorker) Timeout(*river.Job[check.FinalizeCheckArgs]) time.Duration {
	return finalizeTimeout
}

// Work finalizes one batch of a Check whose lanes have ended (ADR 0007): the
// Pages with ids up to the page_id of its next DefaultFinalizeBatchSize
// Fetches. For them it records the page events (the only place they are
// written), updates orphan streaks and adds to the Report counts; it then
// chains the job for the next batch, or, after the last one, marks the Check
// finished, or cancelled when the user asked for that. Once the first batch
// has begun the Check can no longer be cancelled.
//
// It is safe to repeat: a job whose AfterPageID is no longer the Check's
// finalize cursor, or whose Check has ended, does nothing.
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
	if c.State != check.StateRunning && c.State != check.StateCancelling {
		return nil
	}
	switch {
	case !c.FinalizeCursor.Valid && job.Args.AfterPageID == 0:
		if c, err = beginFinalize(ctx, q, c); err != nil {
			return err
		}
	case c.FinalizeCursor.Valid && c.FinalizeCursor.Int64 == job.Args.AfterPageID:
	default:
		return nil // a duplicate or stale job
	}

	after := c.FinalizeCursor.Int64
	upto, err := q.FinalizeBatchEnd(ctx, dbgen.FinalizeBatchEndParams{
		CheckID: c.ID, AfterID: after, BatchSize: int64(cmp.Or(w.finalizeBatchSize, DefaultFinalizeBatchSize)),
	})
	last := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !last {
		return err
	}
	if last { // the rest of the Project's Pages, fetched in the Check or not
		upto = math.MaxInt64
	}
	b := finalizeBatch{c: c, after: after, upto: upto}
	end := check.StateFinished
	switch c.State {
	case check.StateRunning:
		if !c.OrphanCountingSkipped {
			if err := b.countOrphans(ctx, q); err != nil {
				return err
			}
		}
	case check.StateCancelling:
		// The lanes have ended, so no Fetch is in flight. The Check saw only
		// part of the Page pool: skip the rest and leave orphans alone.
		if err := q.SkipPendingFetches(ctx, dbgen.SkipPendingFetchesParams{CheckID: c.ID, AfterID: after, UptoID: upto}); err != nil {
			return err
		}
		end = check.StateCancelled
	}
	if err := b.recordNewPages(ctx, q); err != nil {
		return err
	}
	if err := q.AddFinalizeBatchCounts(ctx, dbgen.AddFinalizeBatchCountsParams{CheckID: c.ID, AfterID: after, UptoID: upto}); err != nil {
		return err
	}
	if last {
		err = q.EndCheck(ctx, dbgen.EndCheckParams{ID: c.ID, State: end})
	} else {
		_, err = river.ClientFromContext[pgx.Tx](ctx).InsertTx(ctx, tx, check.FinalizeCheckArgs{CheckID: c.ID, AfterPageID: upto}, nil)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// beginFinalize starts finalizing c, which from now on can't be cancelled,
// and returns it as updated. A running Check whose seed Page did not end in a
// Snapshot saw no links at all, so it skips orphan counting (and the Report
// says so).
func beginFinalize(ctx context.Context, q *dbgen.Queries, c dbgen.Check) (dbgen.Check, error) {
	if err := q.BeginFinalize(ctx, c.ID); err != nil {
		return c, err
	}
	if c.State == check.StateRunning {
		outcome, err := q.SeedFetchOutcome(ctx, c.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return c, err
		}
		if err != nil || outcome.String != "snapshot" {
			if err := q.MarkOrphanCountingSkipped(ctx, c.ID); err != nil {
				return c, err
			}
		}
	}
	return q.GetCheck(ctx, c.ID)
}

// finalizeBatch is the Pages of a Check's Project with ids in (after, upto].
type finalizeBatch struct {
	c           dbgen.Check
	after, upto int64
}

// recordNewPages records the batch's `new` events (Pages whose first Snapshot
// is in this Check) and `reactivated` ones. Both are recorded for a cancelled
// Check too.
func (b finalizeBatch) recordNewPages(ctx context.Context, q *dbgen.Queries) error {
	if err := q.InsertNewPageEvents(ctx, dbgen.InsertNewPageEventsParams{CheckID: b.c.ID, AfterID: b.after, UptoID: b.upto}); err != nil {
		return err
	}
	return q.InsertReactivatedPageEvents(ctx, dbgen.InsertReactivatedPageEventsParams{CheckID: b.c.ID, AfterID: b.after, UptoID: b.upto})
}

// countOrphans updates the batch's orphan streaks and records its vanished
// and retired events.
func (b finalizeBatch) countOrphans(ctx context.Context, q *dbgen.Queries) error {
	firstFetchID := b.c.FirstFetchID.Int64 // NULL only for a Check without Fetches
	if err := q.ResetLinkedOrphanStreaks(ctx, dbgen.ResetLinkedOrphanStreaksParams{
		CheckID: b.c.ID, ProjectID: b.c.ProjectID, AfterID: b.after, UptoID: b.upto, FirstFetchID: firstFetchID,
	}); err != nil {
		return err
	}
	orphans, err := q.CountOrphans(ctx, dbgen.CountOrphansParams{
		CheckID: b.c.ID, ProjectID: b.c.ProjectID, AfterID: b.after, UptoID: b.upto, FirstFetchID: firstFetchID,
	})
	if err != nil {
		return err
	}
	var vanished, retired []int64
	for _, o := range orphans {
		if o.OrphanStreak == 1 {
			vanished = append(vanished, o.ID)
		}
		if o.OrphanStreak >= page.RetireAfter {
			retired = append(retired, o.ID)
		}
	}
	for kind, ids := range map[string][]int64{page.EventVanished: vanished, page.EventRetired: retired} {
		if len(ids) == 0 {
			continue
		}
		if err := q.InsertPageEvents(ctx, dbgen.InsertPageEventsParams{CheckID: b.c.ID, Kind: kind, PageIds: ids}); err != nil {
			return err
		}
	}
	return nil
}
