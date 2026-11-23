package db_test

import (
	"context"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
)

// The migration that gives a Check running progress counters.
const checkProgressMigration = 45

// Checks that exist when the migration runs keep working: an active Check's
// counters start equal to its rows, and an ended Check shows on the Checks tab
// the Known, Fetched and Failures it showed before, from its stored counts.
func TestCheckProgressMigrationKeepsExistingChecksWorking(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	if _, err := p.DownTo(ctx, checkProgressMigration-1); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain, last_check_number) OVERRIDING SYSTEM VALUE VALUES (1, 1, 'a.example', 2);
		INSERT INTO pages (id, project_id, url, origin, created_at) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 'https://a.example/', 'seed', now() - interval '1 day'),
			(2, 1, 'https://a.example/old', 'discovered', now() - interval '1 day'),
			(3, 1, 'https://a.example/new1', 'discovered', now()),
			(4, 1, 'https://a.example/new2', 'discovered', now()),
			(5, 1, 'https://a.example/new3', 'discovered', now());
		INSERT INTO checks (id, project_id, trigger, state, number, created_at, started_at, finished_at, lanes,
				count_snapshots, count_failures, count_skipped) OVERRIDING SYSTEM VALUE VALUES
			(10, 1, 'manual', 'finished', 1, now() - interval '1 hour', now() - interval '1 hour', now() - interval '50 minutes', 0, 1, 1, 1);
		INSERT INTO checks (id, project_id, trigger, state, number, created_at, started_at, lanes) OVERRIDING SYSTEM VALUE VALUES
			(20, 1, 'manual', 'running', 2, now() - interval '1 minute', now() - interval '1 minute', 1);
		INSERT INTO fetches (check_id, page_id, state, outcome) VALUES
			(10, 1, 'done', 'snapshot'), (10, 2, 'done', 'failure'), (10, 3, 'done', 'skipped'),
			(20, 1, 'done', 'snapshot'), (20, 3, 'done', 'failure'), (20, 4, 'in_flight', NULL), (20, 5, 'pending', NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, checkProgressMigration); err != nil {
		t.Fatal(err)
	}

	var known, fetched, failed, discovered int64
	err = pool.QueryRow(ctx, `SELECT progress_known, progress_fetched, progress_failed, progress_discovered FROM checks WHERE id = 20`).
		Scan(&known, &fetched, &failed, &discovered)
	if err != nil {
		t.Fatal(err)
	}
	if known != 4 || fetched != 2 || failed != 1 || discovered != 3 {
		t.Errorf("active Check counters known %d fetched %d failed %d discovered %d; want 4 2 1 3", known, fetched, failed, discovered)
	}

	rows, err := dbgen.New(pool).ListCheckRows(ctx, dbgen.ListCheckRowsParams{ProjectID: 1, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64][3]int64{}
	for _, r := range rows {
		got[r.ID] = [3]int64{r.Known, r.Fetched, r.Failures}
	}
	want := map[int64][3]int64{10: {3, 3, 1}, 20: {4, 2, 1}}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("Checks tab, Check %d: known/fetched/failures %v, want %v", id, got[id], w)
		}
	}
}
