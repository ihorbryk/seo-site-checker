// Command worker runs the River client that processes jobs.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/app"
	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/ihorbryk/seo-site-checker/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	app.Run("worker", func(ctx context.Context, cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) error {
		client, err := worker.NewClient(pool, worker.Options{Concurrency: cfg.WorkerConcurrency, Log: log})
		if err != nil {
			return err
		}
		if err := client.Start(ctx); err != nil {
			return err
		}
		log.Info("working", "concurrency", cfg.WorkerConcurrency)
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return client.Stop(stopCtx)
	})
}
