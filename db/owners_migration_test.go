package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/db"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// ownersMigration is the migration that gives Projects an owner.
const ownersMigration = 39

// migrateBeforeOwners returns a provider and a database migrated to just
// before Projects got owners, so tests can seed "an existing install".
func migrateBeforeOwners(t *testing.T) (*goose.Provider, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { sqlDB.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, db.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(context.Background(), ownersMigration-1); err != nil {
		t.Fatal(err)
	}
	return p, pool
}

func TestOwnersMigrationNeedsNothingOnAnEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	p, pool := migrateBeforeOwners(t)
	if _, err := p.UpTo(ctx, ownersMigration); err != nil {
		t.Fatalf("empty database without BOOTSTRAP_EMAIL: %v", err)
	}
	var users int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("users = %d, err %v; want none", users, err)
	}
}

func TestOwnersMigrationRefusesExistingProjectsWithoutBootstrapEmail(t *testing.T) {
	ctx := context.Background()
	p, pool := migrateBeforeOwners(t)
	if _, err := pool.Exec(ctx, `INSERT INTO projects (domain) VALUES ('a.example')`); err != nil {
		t.Fatal(err)
	}
	_, err := p.UpTo(ctx, ownersMigration)
	if err == nil || !strings.Contains(err.Error(), "BOOTSTRAP_EMAIL") {
		t.Fatalf("want an error naming BOOTSTRAP_EMAIL, got %v", err)
	}
	// Nothing half-migrated: the migration is transactional.
	var col int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'projects' AND column_name = 'owner_id'`).Scan(&col); err != nil || col != 0 {
		t.Fatalf("owner_id exists after a failed migration (%d, err %v)", col, err)
	}
}

func TestOwnersMigrationGivesExistingProjectsToALockedBootstrapUser(t *testing.T) {
	ctx := context.Background()
	t.Setenv("BOOTSTRAP_EMAIL", "  Boss@Example.com ") // goose reads it when the provider parses the files
	p, pool := migrateBeforeOwners(t)
	if _, err := pool.Exec(ctx, `INSERT INTO projects (domain) VALUES ('a.example'), ('b.example')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, ownersMigration); err != nil {
		t.Fatal(err)
	}
	var email, hash string
	var owned int
	err := pool.QueryRow(ctx, `SELECT u.email, u.password_hash, (SELECT count(*) FROM projects WHERE owner_id = u.id)
		FROM users u`).Scan(&email, &hash, &owned)
	if err != nil {
		t.Fatal(err)
	}
	if email != "boss@example.com" || hash != "!" || owned != 2 {
		t.Fatalf("bootstrap user = %q hash %q owning %d; want boss@example.com, locked, 2", email, hash, owned)
	}
	// Down is best effort, but works here.
	if _, err := p.DownTo(ctx, ownersMigration-1); err != nil {
		t.Fatalf("down: %v", err)
	}
}

func TestProjectsHaveAnImmutableOwnerAndPerOwnerDomains(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	var a, b int64
	for email, id := range map[string]*int64{"a@example.test": &a, "b@example.test": &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, '!') RETURNING id`, email).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(owner int64) error {
		_, err := pool.Exec(ctx, `INSERT INTO projects (owner_id, domain) VALUES ($1, 'x.example')`, owner)
		return err
	}
	if err := insert(a); err != nil {
		t.Fatal(err)
	}
	if err := insert(b); err != nil {
		t.Fatalf("the same domain under another owner: %v", err)
	}
	if err := insert(a); err == nil {
		t.Fatal("the same domain twice under one owner must be refused")
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET owner_id = $1 WHERE owner_id = $2`, b, a); err == nil {
		t.Fatal("changing a Project's owner must be refused")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, a); err == nil {
		t.Fatal("deleting a User who owns Projects must be refused")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (domain) VALUES ('y.example')`); err == nil {
		t.Fatal("a Project needs an owner")
	}
}
