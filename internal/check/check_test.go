package check_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

// Web inserts the Check and its StartCheck job in one transaction: the job is
// there with the transaction, and gone with it.
func TestStartInsertsTheCheckAndStartCheckInOneTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	p := testfixtures.Project(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := check.StartTx(ctx, tx, rc, p.ID, check.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	rivertest.RequireInsertedTx[*riverpgxv5.Driver](ctx, t, tx, &check.StartCheckArgs{CheckID: c.ID}, nil)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	rivertest.RequireNotInserted[*riverpgxv5.Driver](ctx, t, riverpgxv5.New(pool), &check.StartCheckArgs{CheckID: c.ID}, nil)
}

// A second Check for a Project with an active one is refused by the unique
// index, and nothing is inserted for it.
func TestStartRefusesASecondActiveCheck(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	p := testfixtures.Project(t, pool)

	first, err := check.Start(ctx, pool, rc, p.ID, check.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	got, err := check.Start(ctx, pool, rc, p.ID, check.TriggerManual)
	if !errors.Is(err, check.ErrActive) || got.ID != first.ID {
		t.Fatalf("got Check %d, err %v; want the active Check %d and ErrActive", got.ID, err, first.ID)
	}
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'start_check'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("%d StartCheck jobs (err %v), want 1", jobs, err)
	}
}
