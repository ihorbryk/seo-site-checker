package worker_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/worker"
	"github.com/riverqueue/river"
)

// The worker's River client starts against the migrated schema, works a job,
// and stops cleanly.
func TestClientStartsWorksANoopJobAndStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := testdb.New(t)

	client, err := worker.NewClient(pool, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	events, cancelEvents := client.Subscribe(river.EventKindJobCompleted)
	defer cancelEvents()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := client.Insert(ctx, worker.NoopArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	case <-ctx.Done():
		t.Fatal("no-op job was not completed")
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
