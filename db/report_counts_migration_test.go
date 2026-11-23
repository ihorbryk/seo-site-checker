package db_test

import (
	"context"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/testdb"
)

const reportCountsMigration = 46

// The Report counts 00046 adds are backfilled for a Check that ended before
// it by counting every Diff, ignored Pages included, and for a Check that was
// partway through FinalizeCheck only for the Pages it has finalized, so its
// remaining batches complete the counts.
func TestReportCountsMigrationBackfillsEndedAndPartlyFinalizedChecks(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	if _, err := p.DownTo(ctx, reportCountsMigration-1); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain) OVERRIDING SYSTEM VALUE VALUES (1, 1, 'a.example'), (2, 1, 'b.example');
		INSERT INTO pages (id, project_id, url, origin, state) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 'https://a.example/', 'seed', 'active'), (2, 1, 'https://a.example/x', 'discovered', 'ignored'),
			(3, 1, 'https://a.example/y', 'discovered', 'active'), (4, 1, 'https://a.example/z', 'discovered', 'active'),
			(5, 2, 'https://b.example/', 'seed', 'active'), (6, 2, 'https://b.example/x', 'discovered', 'active'),
			(7, 2, 'https://b.example/y', 'discovered', 'active');
		INSERT INTO checks (id, project_id, trigger, state, number, finished_at) OVERRIDING SYSTEM VALUE VALUES
			(10, 1, 'manual', 'finished', 1, now());
		INSERT INTO checks (id, project_id, trigger, state, number, finalize_cursor) OVERRIDING SYSTEM VALUE VALUES
			(20, 2, 'manual', 'running', 1, 6);
		INSERT INTO fetches (id, check_id, page_id, state, outcome, failure_kind) OVERRIDING SYSTEM VALUE VALUES
			(1, 10, 1, 'done', 'failure', 'timeout'), (2, 10, 2, 'done', 'snapshot', NULL),
			(3, 10, 3, 'done', 'snapshot', NULL), (4, 10, 4, 'done', 'snapshot', NULL),
			(5, 20, 5, 'done', 'snapshot', NULL), (6, 20, 6, 'done', 'snapshot', NULL), (7, 20, 7, 'done', 'snapshot', NULL);
		INSERT INTO snapshots (id, page_id, check_id, fetched_at, status, headers, seo_fields) OVERRIDING SYSTEM VALUE VALUES
			(2, 2, 10, now(), 200, '[]', '{}'), (3, 3, 10, now(), 200, '[]', '{}'), (4, 4, 10, now(), 200, '[]', '{}'),
			(5, 5, 20, now(), 200, '[]', '{}'), (6, 6, 20, now(), 200, '[]', '{}'), (7, 7, 20, now(), 200, '[]', '{}');
		INSERT INTO diffs (snapshot_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, tier) VALUES
			(2, 2, false, false, false, false, 'critical'), (3, 3, false, false, false, false, 'empty'), (4, 4, false, false, false, false, 'body'),
			(5, 5, false, false, false, false, 'seo'), (6, 6, false, false, false, false, 'empty'), (7, 7, false, false, false, false, 'headers');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, reportCountsMigration); err != nil {
		t.Fatal(err)
	}

	type counts struct {
		critical, seo, headers, body, empty int
		seedFailed                          bool
	}
	want := map[int64]counts{
		10: {critical: 1, body: 1, empty: 1, seedFailed: true}, // the ignored Page's Diff counts
		20: {seo: 1, empty: 1},                                 // page 7 is past the cursor: its batch adds it
	}
	for id, w := range want {
		var got counts
		err := pool.QueryRow(ctx, `SELECT count_diff_critical, count_diff_seo, count_diff_headers, count_diff_body, count_diff_empty, seed_failed
			FROM checks WHERE id = $1`, id).Scan(&got.critical, &got.seo, &got.headers, &got.body, &got.empty, &got.seedFailed)
		if err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("Check %d: %+v, want %+v", id, got, w)
		}
	}
	if _, err := p.DownTo(ctx, reportCountsMigration-1); err != nil {
		t.Errorf("down: %v", err)
	}
}
