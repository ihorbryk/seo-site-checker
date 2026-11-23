// Package scheduler decides when scheduled Checks are due and enqueues them
// (ADR 0003). It never fetches; it only inserts Checks and StartCheck jobs.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Interval is how often Run calls Tick.
const Interval = 60 * time.Second

// Tick starts a scheduled Check for every Project that is due at now and
// returns how many it started. A Project is due when its schedule isn't
// manual, it has no active Check, and its last scheduled Check's creation time
// plus the interval is not after now. Because only the last scheduled Check
// counts, downtime yields one catch-up Check, never a backfill.
func Tick(ctx context.Context, pool *pgxpool.Pool, rc *river.Client[pgx.Tx], now time.Time, log *slog.Logger) (int, error) {
	due, err := dbgen.New(pool).ListDueProjects(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list due projects: %w", err)
	}
	started := 0
	for _, id := range due {
		switch err := startOne(ctx, pool, rc, id, now); {
		case err == nil:
			started++
		case errors.Is(err, check.ErrActive):
			// A manual Check slipped in since the query; the Project stays due.
		default:
			log.Error("start scheduled Check", "project_id", id, "err", err)
		}
	}
	return started, nil
}

func startOne(ctx context.Context, pool *pgxpool.Pool, rc *river.Client[pgx.Tx], projectID int64, now time.Time) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	c, err := check.StartTx(ctx, tx, rc, projectID, check.TriggerScheduled)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "checks_one_active_idx" {
			return check.ErrActive
		}
		return err
	}
	// The schedule clock is the Check's creation time; make it the tick's now
	// (a deliberate deviation: created_at is the tick time, not the insert time).
	if err := dbgen.New(tx).SetCheckCreatedAt(ctx, dbgen.SetCheckCreatedAtParams{ID: c.ID, CreatedAt: now}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RestartExclusions queues the next ExcludeRecompute job of every Project
// whose recompute is unfinished and has no live job (ADR 0009), and returns
// how many it queued. The Project's exclude_gen, exclude_done_gen and
// exclude_cursor say where to resume, so a discarded job (or one an old worker
// failed during a rollout) loses nothing. A job that runs twice is harmless:
// it only acts if its cursor is still the Project's.
func RestartExclusions(ctx context.Context, pool *pgxpool.Pool, rc *river.Client[pgx.Tx]) (int, error) {
	ids, err := dbgen.New(pool).ListUnfinishedExclusions(ctx)
	if err != nil {
		return 0, fmt.Errorf("list unfinished exclusions: %w", err)
	}
	n := 0
	for _, id := range ids {
		ok, err := restartExclusion(ctx, pool, rc, id)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

func restartExclusion(ctx context.Context, pool *pgxpool.Pool, rc *river.Client[pgx.Tx], projectID int64) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	p, err := dbgen.New(tx).LockProjectForExclusion(ctx, projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if p.ExcludeGen <= p.ExcludeDoneGen {
		return false, nil // finished since the listing
	}
	args := check.ExcludeRecomputeArgs{ProjectID: p.ID, Generation: p.ExcludeGen, AfterPageID: p.ExcludeCursor}
	if _, err := rc.InsertTx(ctx, tx, args, nil); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Housekeep deletes sessions past either expiry. Correctness comes from the
// session lookup; this only keeps the table small. Kept out of Tick so tests
// that tick on a fake clock don't expire real sessions.
func Housekeep(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	_, err := session.Prune(ctx, pool, now)
	return err
}

// Run calls Tick (and Housekeep) every Interval until ctx ends.
func Run(ctx context.Context, pool *pgxpool.Pool, rc *river.Client[pgx.Tx], log *slog.Logger) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		if err := Housekeep(ctx, pool, time.Now()); err != nil {
			log.Error("prune expired sessions", "err", err)
		}
		if n, err := Tick(ctx, pool, rc, time.Now(), log); err != nil {
			log.Error("scheduler tick", "err", err)
		} else if n > 0 {
			log.Info("started scheduled Checks", "count", n)
		}
		if n, err := RestartExclusions(ctx, pool, rc); err != nil {
			log.Error("restart exclude recomputes", "err", err)
		} else if n > 0 {
			log.Info("restarted exclude recomputes", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
