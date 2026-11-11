// Command scheduler decides when Checks are due. For now it connects and idles.
package main

import (
	"context"
	"log/slog"

	"github.com/ihorbryk/seo-site-checker/internal/app"
	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	app.Run("scheduler", func(ctx context.Context, _ config.Config, log *slog.Logger, _ *pgxpool.Pool) error {
		log.Info("idle: no schedules to run yet")
		<-ctx.Done()
		log.Info("stopping")
		return nil
	})
}
