// Package testdb gives each test its own freshly migrated Postgres database.
//
// It needs only DATABASE_URL (a postgres:// URL of a database the role may
// connect to and create databases from; the database itself is just the
// admin connection point). The first call per process builds a template
// database named after a hash of the migrations, applying them with goose; each
// test then gets CREATE DATABASE ... TEMPLATE, dropped on cleanup.
//
// Templates are shared by every process and worktree pointed at the same
// cluster: building is serialised with an advisory lock and published by an
// atomic rename, so concurrent `go test` runs coexist. Templates of older
// migration sets are left behind; drop them with
//
//	psql "$DATABASE_URL" -c "SELECT format('DROP DATABASE %I', datname) FROM pg_database WHERE datname LIKE 'seo_tpl_%'" -t | psql "$DATABASE_URL"
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

const (
	templatePrefix = "seo_tpl_"
	testPrefix     = "seo_test_"
)

var (
	mu        sync.Mutex
	templates = map[string]string{} // admin URL -> template name
)

// New returns a pool on a new database holding the full migrated schema. The
// database is dropped when the test ends. Safe with t.Parallel().
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	admin := adminURL(t)

	tpl, err := template(ctx, admin)
	if err != nil {
		t.Fatalf("testdb: build template: %v", err)
	}
	name := testPrefix + randomHex(8)
	if err := createFromTemplate(ctx, admin, name, tpl); err != nil {
		t.Fatalf("testdb: clone template: %v", err)
	}
	t.Cleanup(func() {
		if err := dropDatabase(context.Background(), admin, name); err != nil {
			t.Errorf("testdb: drop %s: %v", name, err)
		}
	})

	cfg, err := pgxpool.ParseConfig(withDatabase(admin, name))
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	// Registered after the drop, so it runs first (cleanups are LIFO).
	t.Cleanup(pool.Close)
	return pool
}

func adminURL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		t.Fatal("testdb: DATABASE_URL is not set; run through `mise run test` or export it " +
			"(any Postgres 18 where the role can CREATE DATABASE)")
	}
	return u
}

func template(ctx context.Context, admin string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if name, ok := templates[admin]; ok {
		return name, nil
	}
	name, err := ensureTemplate(ctx, admin, db.Migrations())
	if err != nil {
		return "", err
	}
	templates[admin] = name
	return name, nil
}

// ensureTemplate returns the name of the template database for migrations,
// building it if no process has yet.
func ensureTemplate(ctx context.Context, admin string, migrations fs.FS) (string, error) {
	hash, err := hashFS(migrations)
	if err != nil {
		return "", err
	}
	name := templatePrefix + hex.EncodeToString(hash[:8])

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)

	// One builder at a time per cluster-wide admin database; the lock dies with
	// the connection, so a crashed builder never wedges the others.
	lockKey := int64(binary.BigEndian.Uint64(hash[8:16]))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return "", err
	}
	if ok, err := databaseExists(ctx, admin, name); err != nil || ok {
		return name, err
	}

	// Build under a private name and publish by rename: a template either
	// exists complete or not at all, even if this process dies halfway.
	building := name + "_" + randomHex(4)
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+quote(building)); err != nil {
		return "", err
	}
	if err := migrateUp(ctx, withDatabase(admin, building), migrations); err != nil {
		_ = dropDatabase(ctx, admin, building)
		return "", fmt.Errorf("migrate template: %w", err)
	}
	if _, err := conn.Exec(ctx, `ALTER DATABASE `+quote(building)+` RENAME TO `+quote(name)); err != nil {
		_ = dropDatabase(ctx, admin, building)
		// Someone using a different admin database (so a different lock) won.
		if ok, _ := databaseExists(ctx, admin, name); ok {
			return name, nil
		}
		return "", err
	}
	return name, nil
}

func migrateUp(ctx context.Context, dbURL string, migrations fs.FS) error {
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

func createFromTemplate(ctx context.Context, admin, name, tpl string) error {
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	// Postgres refuses to clone a template that has a session open (55006);
	// concurrent clones can briefly trip it, so retry.
	for attempt := 0; ; attempt++ {
		_, err = conn.Exec(ctx, `CREATE DATABASE `+quote(name)+` TEMPLATE `+quote(tpl))
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != "55006" || attempt >= 50 {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func databaseExists(ctx context.Context, admin, name string) (bool, error) {
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return false, err
	}
	defer conn.Close(ctx)
	var ok bool
	err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&ok)
	return ok, err
}

func dropDatabase(ctx context.Context, admin, name string) error {
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `DROP DATABASE IF EXISTS `+quote(name)+` WITH (FORCE)`)
	return err
}

func hashFS(fsys fs.FS) ([32]byte, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
		return nil
	})
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, err
}

func withDatabase(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("testdb: DATABASE_URL must be a postgres:// URL: %v", err))
	}
	u.Path = "/" + name
	return u.String()
}

func quote(ident string) string { return pgx.Identifier{ident}.Sanitize() }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
