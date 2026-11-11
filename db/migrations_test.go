package db_test

import (
	"context"
	"testing"

	"github.com/ihorbryk/seo-site-checker/db"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Every migration's Down must work: tear the schema down to nothing, then
// rebuild it.
func TestMigrationsGoDownToZeroAndBackUp(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, db.Migrations())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down to zero: %v", err)
	}
	var tables int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`).Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("%d tables left after down to zero", tables)
	}

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, table := range []string{"projects", "pages", "river_job", "checks", "fetches", "bodies", "snapshots"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&ok); err != nil || !ok {
			t.Fatalf("table %s missing after migrating back up (err %v)", table, err)
		}
	}
}
