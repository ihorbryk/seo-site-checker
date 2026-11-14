package db_test

import (
	"context"
	"testing"

	"github.com/ihorbryk/seo-site-checker/db"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// checkNumbersMigration is the migration that gives Checks a per-Project number.
const checkNumbersMigration = 40

func TestCheckNumbersMigrationNumbersExistingChecksPerProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { sqlDB.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, db.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, checkNumbersMigration-1); err != nil {
		t.Fatal(err)
	}

	// Two Projects with interleaved Checks, one inserted out of creation
	// order and two sharing a created_at, plus a Project with no Checks.
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'a@example.test', '!');
		INSERT INTO projects (id, owner_id, domain) OVERRIDING SYSTEM VALUE VALUES
			(1, 1, 'a.example'), (2, 1, 'b.example'), (3, 1, 'c.example');
		INSERT INTO checks (id, project_id, trigger, state, created_at) OVERRIDING SYSTEM VALUE VALUES
			(10, 2, 'manual', 'finished', '2026-01-01 10:00Z'),
			(11, 1, 'manual', 'finished', '2026-01-02 10:00Z'),
			(12, 1, 'manual', 'cancelled', '2026-01-01 10:00Z'),
			(13, 2, 'scheduled', 'finished', '2026-01-03 10:00Z'),
			(14, 1, 'manual', 'finished', '2026-01-02 10:00Z'),
			(15, 2, 'manual', 'queued', '2026-01-04 10:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, checkNumbersMigration); err != nil {
		t.Fatal(err)
	}

	want := map[int64]int64{12: 1, 11: 2, 14: 3, 10: 1, 13: 2, 15: 3}
	rows, err := pool.Query(ctx, `SELECT id, number FROM checks`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]int64{}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		got[id] = n
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("Check %d number = %d, want %d", id, got[id], n)
		}
	}

	wantLast := map[int64]int64{1: 3, 2: 3, 3: 0}
	for id, n := range wantLast {
		var last int64
		if err := pool.QueryRow(ctx, `SELECT last_check_number FROM projects WHERE id = $1`, id).Scan(&last); err != nil {
			t.Fatal(err)
		}
		if last != n {
			t.Errorf("Project %d last_check_number = %d, want %d", id, last, n)
		}
	}

	if _, err := pool.Exec(ctx, `INSERT INTO checks (project_id, trigger, number) VALUES (3, 'manual', NULL)`); err == nil {
		t.Error("a Check without a number must be refused")
	}
	if _, err := pool.Exec(ctx, `UPDATE checks SET number = 1 WHERE id = 11`); err == nil {
		t.Error("two Checks with one number in a Project must be refused")
	}
}
