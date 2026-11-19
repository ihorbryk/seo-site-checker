package db_test

import (
	"context"
	"testing"

	"github.com/ihorbryk/seo-site-checker/db"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// The migrations that let a Check plan its Fetches in batches (ADR 0006).
const (
	checkPlanningMigration = 41
	keysetIndexesMigration = 42
)

func migrationProvider(t *testing.T, pool *pgxpool.Pool) *goose.Provider {
	t.Helper()
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { sqlDB.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, db.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Rolling back hands planning Checks to the old StartCheck as queued, and ends
// a Check cancelled while planning, which no old job would ever finalize and
// which would otherwise keep its Project's active slot forever.
func TestCheckPlanningDownLeavesNoCheckTheOldWorkerCannotEnd(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 'a.example'), (2, 1, 'b.example'), (3, 1, 'c.example');
		INSERT INTO pages (id, project_id, url, origin) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 'https://a.example/', 'seed'), (2, 2, 'https://b.example/', 'seed'),
			(3, 2, 'https://b.example/x', 'discovered'), (4, 3, 'https://c.example/', 'seed');
		INSERT INTO checks (id, project_id, trigger, state, number, started_at, plan_cursor, lanes) OVERRIDING SYSTEM VALUE VALUES
			(10, 1, 'manual', 'planning', 1, now(), 1, 0),
			(20, 2, 'manual', 'cancelling', 1, now(), 3, 0),
			(30, 3, 'manual', 'cancelling', 1, now(), 4, 1);
		INSERT INTO fetches (check_id, page_id, state) VALUES
			(10, 1, 'pending'), (20, 2, 'pending'), (20, 3, 'pending'), (30, 4, 'in_flight');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, checkPlanningMigration-1); err != nil {
		t.Fatal(err)
	}

	type row struct {
		state           string
		started, ended  bool
		fetches, skips  int
		countSkippedCol int
	}
	got := map[int64]row{}
	rows, err := pool.Query(ctx, `SELECT c.id, c.state, c.started_at IS NOT NULL, c.finished_at IS NOT NULL,
		(SELECT count(*) FROM fetches f WHERE f.check_id = c.id),
		(SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.skip_reason = 'cancelled' AND f.state = 'done'),
		c.count_skipped
		FROM checks c`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var r row
		if err := rows.Scan(&id, &r.state, &r.started, &r.ended, &r.fetches, &r.skips, &r.countSkippedCol); err != nil {
			t.Fatal(err)
		}
		got[id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[int64]row{
		10: {state: "queued"},                                                                          // replanned by the old StartCheck
		20: {state: "cancelled", started: true, ended: true, fetches: 2, skips: 2, countSkippedCol: 2}, // ended as FinalizeCheck would
		30: {state: "cancelling", started: true, fetches: 1},                                           // its lane will finalize it
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("Check %d after down: %+v, want %+v", id, got[id], w)
		}
	}
}

// 00042 builds its indexes concurrently, outside a transaction. A build that
// was interrupted leaves an INVALID index under the new name; rerunning the
// migration must rebuild it rather than skip it and drop the old valid index.
func TestKeysetIndexesMigrationRebuildsAnIndexAnInterruptedRunLeftInvalid(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := migrationProvider(t, pool)
	if _, err := p.DownTo(ctx, keysetIndexesMigration-1); err != nil {
		t.Fatal(err)
	}
	// What an interrupted CREATE INDEX CONCURRENTLY leaves behind.
	_, err := pool.Exec(ctx, `
		CREATE INDEX fetches_pending_check_id_idx ON fetches (check_id, id) WHERE state = 'pending';
		UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'fetches_pending_check_id_idx'::regclass;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, keysetIndexesMigration); err != nil {
		t.Fatal(err)
	}
	for name, wantValid := range map[string]bool{
		"fetches_pending_check_id_idx": true, "pages_project_state_id_idx": true,
		"fetches_pending_idx": false, "pages_project_state_idx": false,
	} {
		var valid bool
		err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass($1)), false)`, name).Scan(&valid)
		if err != nil || valid != wantValid {
			t.Errorf("index %s valid=%v (err %v), want %v", name, valid, err, wantValid)
		}
	}
}
