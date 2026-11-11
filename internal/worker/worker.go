// Package worker builds the River client that runs Checks: StartCheck, the
// lanes that perform Fetches, and FinalizeCheck (ADR 0003). All network
// access goes through the injected *http.Client.
package worker

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Options configures the worker's River client.
type Options struct {
	// Concurrency is the number of jobs worked at once (WORKER_CONCURRENCY).
	Concurrency int
	Log         *slog.Logger
	// HTTPClient performs every Fetch; it must not follow redirects. Defaults
	// to NewHTTPClient().
	HTTPClient *http.Client
	// FetchPollInterval overrides River's job polling interval (tests lower it).
	FetchPollInterval time.Duration
}

type deps struct {
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	client *http.Client
	log    *slog.Logger
}

// NewClient returns a River client working the default queue. The caller
// starts and stops it.
func NewClient(pool *pgxpool.Pool, opts Options) (*river.Client[pgx.Tx], error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = NewHTTPClient()
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	d := &deps{pool: pool, q: dbgen.New(pool), client: opts.HTTPClient, log: opts.Log}

	workers := river.NewWorkers()
	river.AddWorker(workers, &startWorker{deps: d})
	river.AddWorker(workers, &laneWorker{deps: d})
	river.AddWorker(workers, &finalizeWorker{deps: d})
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: opts.Concurrency}},
		Workers: workers,
		Logger:  opts.Log,
		// ADR 0003: above the lane timeout (fetch timeout + 30 s).
		RescueStuckJobsAfter: 5 * time.Minute,
		FetchPollInterval:    opts.FetchPollInterval,
		FetchCooldown:        opts.FetchPollInterval,
	})
}

// laneTimeout is a lane's River job timeout.
func laneTimeout(fetchTimeoutS int) time.Duration {
	return time.Duration(fetchTimeoutS)*time.Second + 30*time.Second
}
