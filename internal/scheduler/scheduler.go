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

// Run calls Tick every Interval until ctx ends.
func Run(ctx context.Context, pool *pgxpool.Pool, rc *river.Client[pgx.Tx], log *slog.Logger) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		if n, err := Tick(ctx, pool, rc, time.Now(), log); err != nil {
			log.Error("scheduler tick", "err", err)
		} else if n > 0 {
			log.Info("started scheduled Checks", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
