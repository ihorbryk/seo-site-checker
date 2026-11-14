package check_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/jackc/pgx/v5/pgxpool"
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

// Check numbers count from 1 within each Project, whatever other Projects
// have run, and a cancelled Check keeps its number.
func TestStartNumbersChecksPerProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	other, p := testfixtures.Project(t, pool), testfixtures.Project(t, pool)
	testfixtures.Check(t, pool, other)

	first, err := check.Start(ctx, pool, rc, p.ID, check.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE checks SET state = 'cancelled' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := check.Start(ctx, pool, rc, p.ID, check.TriggerScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if first.Number != 1 || second.Number != 2 {
		t.Fatalf("numbers %d, %d; want 1, 2", first.Number, second.Number)
	}
}

// Two Checks created at once in one Project get consecutive numbers: the
// second waits for the first's transaction on the Project row.
func TestConcurrentStartsGetConsecutiveNumbers(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	p := testfixtures.Project(t, pool)

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(ctx) //nolint:errcheck // no-op after commit
	first, err := check.StartTx(ctx, tx1, rc, p.ID, check.TriggerScheduled)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		c   dbgen.Check
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := check.Start(ctx, pool, rc, p.ID, check.TriggerManual)
		done <- result{c, err}
	}()
	waitForBlockedLock(t, pool)
	// The first Check ends before its transaction commits, so the second
	// is not refused as a second active Check once it gets the lock.
	if _, err := tx1.Exec(ctx, `UPDATE checks SET state = 'cancelled' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	second := <-done
	if second.err != nil {
		t.Fatal(second.err)
	}
	if first.Number != 1 || second.c.Number != 2 {
		t.Fatalf("numbers %d, %d; want 1, 2", first.Number, second.c.Number)
	}
}

// waitForBlockedLock waits until some session waits for a lock.
func waitForBlockedLock(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the second Start never waited for the Project row")
}
