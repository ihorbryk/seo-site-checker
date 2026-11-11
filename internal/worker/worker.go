// Package worker builds the River client that processes jobs. It has no real
// job kinds yet; a no-op one keeps River's client configuration valid.
package worker

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// NoopArgs is a job that does nothing.
type NoopArgs struct{}

func (NoopArgs) Kind() string { return "noop" }

type noopWorker struct{ river.WorkerDefaults[NoopArgs] }

func (noopWorker) Work(context.Context, *river.Job[NoopArgs]) error { return nil }

// NewClient returns a River client working the default queue with up to
// concurrency jobs at once. The caller starts and stops it.
func NewClient(pool *pgxpool.Pool, concurrency int, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &noopWorker{})
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: concurrency}},
		Workers: workers,
		Logger:  log,
	})
}
