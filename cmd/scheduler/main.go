// Command scheduler decides when Checks are due and enqueues them. It never
// fetches; it is an insert-only River client (ADR 0003).
package main

import (
	"context"
	"log/slog"

	"github.com/ihorbryk/seo-site-checker/internal/app"
	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/ihorbryk/seo-site-checker/internal/scheduler"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	app.Run("scheduler", func(ctx context.Context, _ config.Config, log *slog.Logger, pool *pgxpool.Pool) error {
		rc, err := check.NewInsertClient(pool)
		if err != nil {
			return err
		}
		log.Info("running", "interval", scheduler.Interval)
		scheduler.Run(ctx, pool, rc, log)
		log.Info("stopping")
		return nil
	})
}
