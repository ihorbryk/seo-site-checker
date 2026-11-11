package testdb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func insertProject(t *testing.T, domain string) {
	t.Helper()
	pool := New(t)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO projects (domain) VALUES ($1)`, domain); err != nil {
		t.Fatalf("insert into migrated schema: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("fresh database should hold only this test's row, got %d projects", n)
	}
}

// Each test gets its own migrated database; the same domain can be inserted
// in parallel tests because they never share rows.
func TestNewGivesIsolatedMigratedDatabases(t *testing.T) {
	for i := 0; i < 4; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			insertProject(t, "example.com")
		})
	}
}

func TestDatabaseIsDroppedOnCleanup(t *testing.T) {
	var name string
	t.Run("inner", func(t *testing.T) {
		pool := New(t)
		name = pool.Config().ConnConfig.Database
	})
	exists, err := databaseExists(context.Background(), adminURL(t), name)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("database %s still exists after the test finished", name)
	}
}

// Several processes (or worktrees) may build the same template at once; all of
// them must end up with one complete template.
func TestConcurrentTemplateCreation(t *testing.T) {
	stamp := time.Now().UnixNano()
	migrations := fstest.MapFS{
		"00001_t.sql": {Data: []byte(fmt.Sprintf(
			"-- +goose Up\nCREATE TABLE t_%d (id int);\n-- +goose Down\nDROP TABLE t_%d;\n", stamp, stamp))},
	}
	ctx := context.Background()
	url := adminURL(t)
	const n = 6
	names := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			names[i], errs[i] = ensureTemplate(ctx, url, migrations)
		}()
	}
	wg.Wait()
	t.Cleanup(func() { _ = dropDatabase(ctx, url, names[0]) })
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("builder %d: %v", i, errs[i])
		}
		if names[i] != names[0] {
			t.Fatalf("template names differ: %q vs %q", names[i], names[0])
		}
	}
	ok, err := databaseExists(ctx, url, names[0])
	if err != nil || !ok {
		t.Fatalf("template missing: %v %v", ok, err)
	}
}
