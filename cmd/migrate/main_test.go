package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/ihorbryk/seo-site-checker/db"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func provider(t *testing.T, sqlDB *sql.DB) *goose.Provider {
	t.Helper()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, db.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMigratesAnEmptyDatabaseToLatestAndASecondRunIsANoOp(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if _, err := provider(t, sqlDB).DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	n, err := migrate(ctx, sqlDB, quiet)
	if err != nil || n == 0 {
		t.Fatalf("first run applied %d, err %v; want some, nil", n, err)
	}
	latest, err := provider(t, sqlDB).GetDBVersion(ctx)
	if err != nil || latest == 0 {
		t.Fatalf("version %d, err %v", latest, err)
	}
	n, err = migrate(ctx, sqlDB, quiet)
	if err != nil || n != 0 {
		t.Fatalf("second run applied %d, err %v; want 0, nil", n, err)
	}
}

// The provider substitutes ${BOOTSTRAP_EMAIL} in migration 00039 (ENVSUB ON).
func TestMigrateHonoursBootstrapEmail(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	t.Setenv("BOOTSTRAP_EMAIL", "Boss@Example.com")
	if _, err := provider(t, sqlDB).DownTo(ctx, 38); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (domain) VALUES ('a.example')`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(ctx, sqlDB, quiet); err != nil {
		t.Fatal(err)
	}
	var email string
	if err := pool.QueryRow(ctx, `SELECT u.email FROM users u JOIN projects p ON p.owner_id = u.id`).Scan(&email); err != nil || email != "boss@example.com" {
		t.Fatalf("owner %q, err %v; want boss@example.com", email, err)
	}
}

func TestMigrateFailsWhenAMigrationFails(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	t.Setenv("BOOTSTRAP_EMAIL", "")
	if _, err := provider(t, sqlDB).DownTo(ctx, 38); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (domain) VALUES ('a.example')`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(ctx, sqlDB, quiet); err == nil {
		t.Fatal("want an error: existing projects and no BOOTSTRAP_EMAIL")
	}
}
