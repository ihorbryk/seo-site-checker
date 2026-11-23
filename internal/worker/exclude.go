package worker

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// DefaultExcludeBatchSize is how many Pages one ExcludeRecompute job handles
// (ADR 0009). A batch is a fraction of a second however large the pool grows.
const DefaultExcludeBatchSize = 20_000

// excludeTimeout is an ExcludeRecompute job's River timeout; a batch takes
// well under it. It must stay below rescueStuckJobsAfter.
const excludeTimeout = 2 * time.Minute

type excludeWorker struct {
	river.WorkerDefaults[check.ExcludeRecomputeArgs]
	*deps
}

func (w *excludeWorker) Timeout(*river.Job[check.ExcludeRecomputeArgs]) time.Duration {
	return excludeTimeout
}

// Work recomputes the stored exclusion answer of one batch of the Project's
// Pages (ADR 0009): the next DefaultExcludeBatchSize Pages by id after the
// job's AfterPageID, against the Project's current patterns. It advances the
// Project's exclude cursor and, in the same transaction, enqueues the job for
// the next batch.
//
// It holds the Project row lock for the batch, so a save of the patterns
// waits for it: after the save commits, no batch of an older generation is in
// flight, and its next job sees the new generation and stops.
//
// It is safe to repeat: a job whose Generation is no longer the Project's, or
// whose AfterPageID is no longer its exclude cursor, does nothing.
func (w *excludeWorker) Work(ctx context.Context, job *river.Job[check.ExcludeRecomputeArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := w.q.WithTx(tx)

	p, err := q.LockProjectForExclusion(ctx, job.Args.ProjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the Project is gone
		}
		return err
	}
	if p.ExcludeGen != job.Args.Generation || p.ExcludeCursor != job.Args.AfterPageID {
		return nil // a duplicate, or a chain a newer save replaced
	}
	size := cmp.Or(w.excludeBatchSize, DefaultExcludeBatchSize)
	batch, err := q.ListExcludeBatch(ctx, dbgen.ListExcludeBatchParams{
		ProjectID: p.ID, AfterID: p.ExcludeCursor, BatchSize: int32(size),
	})
	if err != nil {
		return err
	}
	ids := make([]int64, len(batch))
	flags := make([]bool, len(batch))
	for i, pg := range batch {
		ids[i], flags[i] = pg.ID, page.Excluded(p.ExcludePatterns, pg.Origin, pg.Url)
	}
	if len(ids) > 0 {
		if err := q.SetPagesExcluded(ctx, dbgen.SetPagesExcludedParams{PageIds: ids, Flags: flags}); err != nil {
			return err
		}
		last := ids[len(ids)-1]
		if err := q.SetExcludeCursor(ctx, dbgen.SetExcludeCursorParams{ID: p.ID, ExcludeCursor: last}); err != nil {
			return err
		}
		if len(batch) == size {
			rc := river.ClientFromContext[pgx.Tx](ctx)
			next := check.ExcludeRecomputeArgs{ProjectID: p.ID, Generation: p.ExcludeGen, AfterPageID: last}
			if _, err := rc.InsertTx(ctx, tx, next, nil); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}
