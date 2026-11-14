package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// ownersMigration is the migration that gives Projects an owner.
const ownersMigration = 39

func provider(t *testing.T, sqlDB *sql.DB) *goose.Provider {
	t.Helper()
	p, err := newProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// beforeOwnersWithProject returns a database migrated to just before Projects
// got owners, holding one ownerless Project. Set env vars before calling:
// goose reads them when the provider parses the files.
func beforeOwnersWithProject(t *testing.T) (*sql.DB, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { sqlDB.Close() })
	if _, err := provider(t, sqlDB).DownTo(ctx, ownersMigration-1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (domain) VALUES ('a.example')`); err != nil {
		t.Fatal(err)
	}
	return sqlDB, pool
}

func TestMigratesAnEmptyDatabaseToLatestAndASecondRunIsANoOp(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if _, err := provider(t, sqlDB).DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	n, err := applyPending(ctx, sqlDB, quiet)
	if err != nil || n == 0 {
		t.Fatalf("first run applied %d, err %v; want some, nil", n, err)
	}
	latest, err := provider(t, sqlDB).GetDBVersion(ctx)
	if err != nil || latest == 0 {
		t.Fatalf("version %d, err %v", latest, err)
	}
	n, err = applyPending(ctx, sqlDB, quiet)
	if err != nil || n != 0 {
		t.Fatalf("second run applied %d, err %v; want 0, nil", n, err)
	}
}

// The provider substitutes ${BOOTSTRAP_EMAIL} in the owners migration (ENVSUB ON).
func TestMigrateHonoursBootstrapEmail(t *testing.T) {
	ctx := context.Background()
	t.Setenv("BOOTSTRAP_EMAIL", "Boss@Example.com")
	sqlDB, pool := beforeOwnersWithProject(t)
	if _, err := applyPending(ctx, sqlDB, quiet); err != nil {
		t.Fatal(err)
	}
	var email string
	if err := pool.QueryRow(ctx, `SELECT u.email FROM users u JOIN projects p ON p.owner_id = u.id`).Scan(&email); err != nil || email != "boss@example.com" {
		t.Fatalf("owner %q, err %v; want boss@example.com", email, err)
	}
}

func TestMigrateFailsWhenAMigrationFails(t *testing.T) {
	t.Setenv("BOOTSTRAP_EMAIL", "")
	sqlDB, _ := beforeOwnersWithProject(t)
	if _, err := applyPending(context.Background(), sqlDB, quiet); err == nil {
		t.Fatal("want an error: existing projects and no BOOTSTRAP_EMAIL")
	}
}
