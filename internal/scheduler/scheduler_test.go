package scheduler_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/scheduler"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

var t0 = time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	rc   *river.Client[pgx.Tx]
}

func newEnv(t *testing.T) *env {
	pool := testdb.New(t)
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t, pool, rc}
}

// tick runs the scheduler at now and returns how many Checks it started.
func (e *env) tick(now time.Time) int {
	e.t.Helper()
	n, err := scheduler.Tick(context.Background(), e.pool, e.rc, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) checks(projectID int64) []dbgen.Check {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT * FROM checks WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		e.t.Fatal(err)
	}
	cs, err := pgx.CollectRows(rows, pgx.RowToStructByName[dbgen.Check])
	if err != nil {
		e.t.Fatal(err)
	}
	return cs
}

func (e *env) setState(checkID int64, state string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE checks SET state = $2 WHERE id = $1`, checkID, state); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) manualCheck(projectID int64, state string, at time.Time) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO checks (project_id, trigger, state, created_at) VALUES ($1, 'manual', $2, $3)`, projectID, state, at); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) requireStartJob(c dbgen.Check) {
	e.t.Helper()
	rivertest.RequireInsertedTx[*riverpgxv5.Driver](context.Background(), e.t, e.mustBegin(), &check.StartCheckArgs{CheckID: c.ID}, nil)
}

func (e *env) mustBegin() pgx.Tx {
	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func TestFirstCheckIsDueImmediatelyAndNotAgainBeforeTheInterval(t *testing.T) {
	e := newEnv(t)
	p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("hourly"))

	if n := e.tick(t0); n != 1 {
		t.Fatalf("first tick started %d Checks, want 1", n)
	}
	cs := e.checks(p.ID)
	if len(cs) != 1 || cs[0].Trigger != check.TriggerScheduled || cs[0].State != check.StateQueued {
		t.Fatalf("checks = %+v, want one queued scheduled Check", cs)
	}
	if !cs[0].CreatedAt.Equal(t0) {
		t.Errorf("created_at = %v, want the tick's now %v", cs[0].CreatedAt, t0)
	}
	e.requireStartJob(cs[0])

	e.setState(cs[0].ID, check.StateFinished)
	if n := e.tick(t0.Add(59*time.Minute + 59*time.Second)); n != 0 {
		t.Fatalf("tick just before the interval started %d Checks", n)
	}
	if n := e.tick(t0.Add(time.Hour)); n != 1 {
		t.Fatalf("tick at created_at + interval started %d Checks, want 1", n)
	}
}

func TestIntervalsPerSchedule(t *testing.T) {
	for _, tc := range []struct {
		schedule string
		interval time.Duration
	}{
		{"hourly", time.Hour},
		{"daily", 24 * time.Hour},
		{"weekly", 7 * 24 * time.Hour},
	} {
		t.Run(tc.schedule, func(t *testing.T) {
			e := newEnv(t)
			p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule(tc.schedule))
			e.tick(t0)
			e.setState(e.checks(p.ID)[0].ID, check.StateFinished)

			if n := e.tick(t0.Add(tc.interval - time.Second)); n != 0 {
				t.Errorf("due one second early")
			}
			if n := e.tick(t0.Add(tc.interval)); n != 1 {
				t.Errorf("not due at the interval")
			}
		})
	}
}

func TestManualOnlyProjectsAreNeverDue(t *testing.T) {
	e := newEnv(t)
	p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("manual"))

	if n := e.tick(t0.Add(1000 * time.Hour)); n != 0 || len(e.checks(p.ID)) != 0 {
		t.Fatalf("manual-only Project got %d Checks", n)
	}
	rivertest.RequireNotInserted[*riverpgxv5.Driver](context.Background(), t, riverpgxv5.New(e.pool), &check.StartCheckArgs{}, nil)
}

func TestManualChecksDontMoveTheClock(t *testing.T) {
	e := newEnv(t)
	p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("hourly"))
	e.tick(t0)
	e.setState(e.checks(p.ID)[0].ID, check.StateFinished)
	e.manualCheck(p.ID, check.StateFinished, t0.Add(50*time.Minute))

	if n := e.tick(t0.Add(time.Hour)); n != 1 {
		t.Fatalf("a manual Check at +50m delayed the scheduled one: started %d", n)
	}
}

func TestAProjectWithOnlyManualChecksIsStillDueForItsFirstScheduledCheck(t *testing.T) {
	e := newEnv(t)
	p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("daily"))
	e.manualCheck(p.ID, check.StateFinished, t0.Add(-time.Minute))

	if n := e.tick(t0); n != 1 {
		t.Fatalf("started %d, want 1", n)
	}
}

func TestACancelledScheduledCheckStillCountsForTheClock(t *testing.T) {
	e := newEnv(t)
	p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("hourly"))
	e.tick(t0)
	e.setState(e.checks(p.ID)[0].ID, check.StateCancelled)

	if n := e.tick(t0.Add(30 * time.Minute)); n != 0 {
		t.Fatalf("cancelled Check didn't count: started %d", n)
	}
	if n := e.tick(t0.Add(time.Hour)); n != 1 {
		t.Fatalf("started %d at the next slot, want 1", n)
	}
}

func TestLongDowntimeCreatesExactlyOneCatchUpCheck(t *testing.T) {
	e := newEnv(t)
	p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("hourly"))
	e.tick(t0)
	e.setState(e.checks(p.ID)[0].ID, check.StateFinished)

	back := t0.Add(10 * time.Hour) // ten missed slots
	if n := e.tick(back); n != 1 {
		t.Fatalf("catch-up started %d Checks, want 1", n)
	}
	if got := len(e.checks(p.ID)); got != 2 {
		t.Fatalf("%d Checks, want 2 (no backfill)", got)
	}
	e.setState(e.checks(p.ID)[1].ID, check.StateFinished)
	if n := e.tick(back.Add(time.Minute)); n != 0 {
		t.Fatalf("a second catch-up started %d", n)
	}
	if n := e.tick(back.Add(time.Hour)); n != 1 {
		t.Fatalf("schedule resumes from the catch-up Check: started %d", n)
	}
}

func TestADueProjectWithAnActiveCheckWaitsThenStartsOnTheFirstTickAfter(t *testing.T) {
	e := newEnv(t)
	p := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("hourly"))
	e.tick(t0)
	e.setState(e.checks(p.ID)[0].ID, check.StateFinished)
	e.manualCheck(p.ID, check.StateRunning, t0.Add(59*time.Minute))

	for _, d := range []time.Duration{time.Hour, 2 * time.Hour} {
		if n := e.tick(t0.Add(d)); n != 0 {
			t.Fatalf("started a Check at +%v while one is active", d)
		}
	}
	if got := len(e.checks(p.ID)); got != 2 {
		t.Fatalf("%d Checks, want 2", got)
	}

	e.setState(e.checks(p.ID)[1].ID, check.StateFinished)
	if n := e.tick(t0.Add(2*time.Hour + time.Minute)); n != 1 {
		t.Fatalf("started %d after the active Check ended, want 1", n)
	}
}

func TestOnlyDueProjectsGetChecksAndNothingIsInsertedOtherwise(t *testing.T) {
	e := newEnv(t)
	due := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("daily"))
	paused := testfixtures.Project(t, e.pool, testfixtures.WithSchedule("manual"))

	if n := e.tick(t0); n != 1 {
		t.Fatalf("started %d, want 1", n)
	}
	e.requireStartJob(e.checks(due.ID)[0])
	if len(e.checks(paused.ID)) != 0 {
		t.Fatal("paused Project got a Check")
	}
	var jobs int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = 'start_check'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("%d StartCheck jobs (err %v), want 1", jobs, err)
	}
}
