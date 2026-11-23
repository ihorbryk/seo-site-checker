package db_test

import (
	"context"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/testdb"
)

const reportSectionIndexesMigration = 48

// Diffs that exist before 00048 get the Check of their Snapshot, and the
// indexes the Report sections read through are built and valid; Down removes
// both, and a rerun after it works.
func TestReportSectionIndexesMigrationBackfillsDiffChecksAndIsRerunnable(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	if _, err := p.DownTo(ctx, reportSectionIndexesMigration-1); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain) OVERRIDING SYSTEM VALUE VALUES (1, 1, 'a.example');
		INSERT INTO pages (id, project_id, url, origin, state) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 'https://a.example/', 'seed', 'active'), (2, 1, 'https://a.example/x', 'discovered', 'active');
		INSERT INTO checks (id, project_id, trigger, state, number, finished_at) OVERRIDING SYSTEM VALUE VALUES
			(10, 1, 'manual', 'finished', 1, now()), (20, 1, 'manual', 'finished', 2, now());
		INSERT INTO fetches (id, check_id, page_id, state, outcome) OVERRIDING SYSTEM VALUE VALUES
			(1, 10, 1, 'done', 'snapshot'), (2, 20, 1, 'done', 'snapshot'), (3, 20, 2, 'done', 'snapshot');
		INSERT INTO snapshots (id, page_id, check_id, fetched_at, status, headers, seo_fields) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 10, now(), 200, '[]', '{}'), (2, 1, 20, now(), 200, '[]', '{}'), (3, 2, 20, now(), 200, '[]', '{}');
		INSERT INTO diffs (snapshot_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, tier) VALUES
			(1, 1, false, false, false, false, 'body'), (2, 1, false, false, false, false, 'empty'), (3, 3, false, false, false, false, 'seo');`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := p.UpTo(ctx, reportSectionIndexesMigration); err != nil {
			t.Fatal(err)
		}
		rows, err := pool.Query(ctx, `SELECT snapshot_id, check_id FROM diffs ORDER BY snapshot_id`)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]int64{}
		for rows.Next() {
			var s, c int64
			if err := rows.Scan(&s, &c); err != nil {
				t.Fatal(err)
			}
			got[s] = c
		}
		rows.Close()
		if len(got) != 3 || got[1] != 10 || got[2] != 20 || got[3] != 20 {
			t.Errorf("diff checks (snapshot -> check) = %v, want 1:10 2:20 3:20", got)
		}
		// An old worker's Diff insert (no check_id) works against the new
		// schema and gets the Snapshot's Check.
		if _, err := pool.Exec(ctx, `
			INSERT INTO fetches (id, check_id, page_id, state, outcome) OVERRIDING SYSTEM VALUE VALUES (4, 10, 2, 'done', 'snapshot');
			INSERT INTO snapshots (id, page_id, check_id, fetched_at, status, headers, seo_fields) OVERRIDING SYSTEM VALUE VALUES (4, 2, 10, now(), 200, '[]', '{}');
			INSERT INTO diffs (snapshot_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, tier)
			VALUES (4, 3, false, false, false, false, 'body');`); err != nil {
			t.Fatalf("insert without check_id: %v", err)
		}
		var oldInsert int64
		if err := pool.QueryRow(ctx, `SELECT check_id FROM diffs WHERE snapshot_id = 4`).Scan(&oldInsert); err != nil || oldInsert != 10 {
			t.Errorf("check_id of an old-style insert = %d (err %v), want 10", oldInsert, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM snapshots WHERE id = 4; DELETE FROM fetches WHERE id = 4`); err != nil {
			t.Fatal(err)
		}
		var valid int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE i.indisvalid AND c.relname IN ('diffs_check_tier_idx', 'fetches_check_outcome_idx', 'page_events_check_kind_idx')`).Scan(&valid); err != nil || valid != 3 {
			t.Errorf("valid section indexes = %d (err %v), want 3", valid, err)
		}
		if _, err := p.DownTo(ctx, reportSectionIndexesMigration-1); err != nil {
			t.Fatalf("down: %v", err)
		}
		var left int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relname IN ('diffs_check_tier_idx', 'fetches_check_outcome_idx', 'page_events_check_kind_idx')`).Scan(&left); err != nil || left != 0 {
			t.Errorf("indexes left after down = %d (err %v)", left, err)
		}
	}
}

// The backfill runs in batches that commit on their own, so it must cross
// batch boundaries without skipping a Diff.
func TestReportSectionIndexesMigrationBackfillsManyDiffs(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	if _, err := p.DownTo(ctx, reportSectionIndexesMigration-1); err != nil {
		t.Fatal(err)
	}
	// 25,000 Snapshots, half of them in each Check: three batches.
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain) OVERRIDING SYSTEM VALUE VALUES (1, 1, 'a.example');
		INSERT INTO pages (id, project_id, url, origin, state) OVERRIDING SYSTEM VALUE
			SELECT g, 1, 'https://a.example/' || g, 'discovered', 'active' FROM generate_series(1, 25000) g;
		INSERT INTO checks (id, project_id, trigger, state, number, finished_at) OVERRIDING SYSTEM VALUE VALUES
			(10, 1, 'manual', 'finished', 1, now()), (20, 1, 'manual', 'finished', 2, now());
		INSERT INTO fetches (id, check_id, page_id, state, outcome) OVERRIDING SYSTEM VALUE
			SELECT g, CASE WHEN g % 2 = 0 THEN 10 ELSE 20 END, g, 'done', 'snapshot' FROM generate_series(1, 25000) g;
		INSERT INTO snapshots (id, page_id, check_id, fetched_at, status, headers, seo_fields)
			OVERRIDING SYSTEM VALUE SELECT g, g, CASE WHEN g % 2 = 0 THEN 10 ELSE 20 END, now(), 200, '[]', '{}' FROM generate_series(1, 25000) g;
		INSERT INTO diffs (snapshot_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, tier)
			SELECT id, id, false, false, false, false, 'empty' FROM snapshots;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, reportSectionIndexesMigration); err != nil {
		t.Fatal(err)
	}
	var wrong int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id WHERE d.check_id IS DISTINCT FROM s.check_id`).Scan(&wrong); err != nil || wrong != 0 {
		t.Errorf("%d Diffs with the wrong check_id (err %v)", wrong, err)
	}
}
