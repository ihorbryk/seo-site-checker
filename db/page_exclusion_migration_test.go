package db_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/worker"
)

// The migration that stores whether a Page is excluded (ADR 0009).
const pageExclusionMigration = 45

// Pages that exist before the migration are classified afterwards with no
// manual step: the migration queues a recompute for every Project that has
// exclude patterns, and the worker finishes it.
func TestPageExclusionMigrationClassifiesExistingPagesWithoutAnOperator(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	if _, err := p.DownTo(ctx, pageExclusionMigration-1); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain, exclude_patterns) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 'a.example', '{"/private/**"}'), (2, 1, 'b.example', '{}');
		INSERT INTO pages (project_id, url, origin) VALUES
			(1, 'https://a.example/', 'seed'), (1, 'https://a.example/private/x', 'discovered'),
			(1, 'https://a.example/open', 'discovered'), (2, 'https://b.example/private/x', 'discovered');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, pageExclusionMigration); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'exclude_recompute'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("%d recompute jobs, err %v; want one, for the Project with patterns", jobs, err)
	}
	// Down and up again queues one job again, not a second one.
	if _, err := p.DownTo(ctx, pageExclusionMigration-1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, pageExclusionMigration); err != nil {
		t.Fatal(err)
	}

	w, err := worker.NewClient(pool, worker.Options{Concurrency: 2, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), FetchPollInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.StopAndCancel(context.WithoutCancel(ctx)) })

	deadline := time.Now().Add(20 * time.Second)
	for {
		var got string
		err := pool.QueryRow(ctx, `SELECT coalesce(string_agg(url, ',' ORDER BY url), '') FROM pages WHERE excluded`).Scan(&got)
		if err != nil {
			t.Fatal(err)
		}
		if got == "https://a.example/private/x" {
			return // b.example has no patterns, so its Page is not excluded
		}
		if time.Now().After(deadline) {
			t.Fatalf("excluded Pages = %q after the worker ran", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Rolling back drops the columns, the indexes and any queued recompute.
func TestPageExclusionMigrationDownLeavesNoTraces(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain, exclude_patterns) OVERRIDING SYSTEM VALUE VALUES (1, 1, 'a.example', '{"/x"}');
		INSERT INTO river_job (kind, max_attempts, args) VALUES ('exclude_recompute', 5, '{"project_id":1,"generation":1}');`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, pageExclusionMigration-1); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM information_schema.columns WHERE (table_name, column_name) IN
			(('pages', 'excluded'), ('projects', 'exclude_gen'), ('projects', 'exclude_cursor')))
		+ (SELECT count(*) FROM pg_indexes WHERE indexname LIKE 'pages_pool_%' OR indexname = 'pages_project_id_idx')
		+ (SELECT count(*) FROM river_job WHERE kind = 'exclude_recompute')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d leftovers after Down, err %v", left, err)
	}
	if _, err := p.UpTo(ctx, pageExclusionMigration); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
