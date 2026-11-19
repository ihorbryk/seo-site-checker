package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

// planRig is a queued Check whose StartCheck jobs the test works one by one.
type planRig struct {
	t       *testing.T
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	rc      *river.Client[pgx.Tx]
	tx      poolTx
	project dbgen.Project
	checkID int64
	startW  *rivertest.Worker[check.StartCheckArgs, pgx.Tx]
	finW    *rivertest.Worker[check.FinalizeCheckArgs, pgx.Tx]
}

// newPlanRig makes a Project with a seed Page plus n discovered Pages
// (/p/1 ... /p/n, in id order), lets setup adjust it, and queues a Check
// whose StartCheck jobs plan batch Pages each.
func newPlanRig(t *testing.T, n, batch int, setup func(*pgxpool.Pool, dbgen.Project)) *planRig {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	p := testfixtures.Project(t, pool, testfixtures.WithDomain("shop.test"))
	testfixtures.Page(t, pool, p, "/", testfixtures.WithOrigin(page.OriginSeed))
	if _, err := pool.Exec(ctx, `INSERT INTO pages (project_id, url, origin)
		SELECT $1, 'https://shop.test/p/' || g, $3 FROM generate_series(1, $2::int) g ORDER BY g`,
		p.ID, n, page.OriginDiscovered); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(pool, p)
	}
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	c, err := check.Start(ctx, pool, rc, p.ID, check.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &deps{pool: pool, q: dbgen.New(pool), client: siteClient(t), log: log, planBatchSize: batch}
	driver := riverpgxv5.New(pool)
	cfg := func() *river.Config { return &river.Config{Logger: log} }
	return &planRig{
		t: t, pool: pool, q: d.q, rc: rc, tx: newPoolTx(t, pool), project: p, checkID: c.ID,
		startW: rivertest.NewWorker[check.StartCheckArgs, pgx.Tx](t, driver, cfg(), &startWorker{deps: d}),
		finW:   rivertest.NewWorker[check.FinalizeCheckArgs, pgx.Tx](t, driver, cfg(), &finalizeWorker{deps: d}),
	}
}

func (r *planRig) count(sql string, args ...any) int64 {
	r.t.Helper()
	var n int64
	if err := r.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *planRig) fetches() int64 {
	return r.count(`SELECT count(*) FROM fetches WHERE check_id = $1`, r.checkID)
}

func (r *planRig) jobs(kind string) int64 {
	return r.count(`SELECT count(*) FROM river_job WHERE kind = $1 AND (args->>'check_id')::bigint = $2`, kind, r.checkID)
}

// workNext works the oldest available job of kind for the Check; it reports
// false when there is none.
func (r *planRig) workNext(kind string) bool {
	r.t.Helper()
	ctx := context.Background()
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM river_job
		WHERE kind = $1 AND state = 'available' AND (args->>'check_id')::bigint = $2 ORDER BY id LIMIT 1`, kind, r.checkID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		r.t.Fatal(err)
	}
	job, err := r.rc.JobGet(ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	switch kind {
	case check.StartCheckArgs{}.Kind():
		_, err = r.startW.WorkJob(ctx, r.t, r.tx, job)
	case check.FinalizeCheckArgs{}.Kind():
		_, err = r.finW.WorkJob(ctx, r.t, r.tx, job)
	}
	if err != nil {
		r.t.Fatalf("%s job %d: %v", kind, id, err)
	}
	return true
}

// plan works StartCheck jobs until none is left and returns how many ran and
// the most Fetches any one of them planned.
func (r *planRig) plan() (jobs int, most int64) {
	r.t.Helper()
	for before := r.fetches(); r.workNext(check.StartCheckArgs{}.Kind()); before = r.fetches() {
		jobs++
		most = max(most, r.fetches()-before)
	}
	return jobs, most
}

func (r *planRig) state() string {
	r.t.Helper()
	c, err := r.q.GetCheck(context.Background(), r.checkID)
	if err != nil {
		r.t.Fatal(err)
	}
	return c.State
}

// The bug: StartCheck planned one Fetch per Page in a single job, so on a 2M
// Page pool it outran River's job timeout on every attempt ("timeout: context
// deadline exceeded") and the Check never started. Now no job plans more than
// one batch, whatever the size of the pool, and the batches add up to it.
func TestALargePoolStartsThroughJobsOfAtMostOneBatchEach(t *testing.T) {
	r := newPlanRig(t, 50_000, 5_000, nil)
	jobs, most := r.plan()
	if r.state() != check.StateRunning {
		t.Fatalf("Check is %s, want running", r.state())
	}
	if got := r.fetches(); got != 50_001 {
		t.Errorf("%d Fetches planned, want 50001", got)
	}
	if jobs != 11 || most > 5_000 {
		t.Errorf("%d StartCheck jobs, at most %d Fetches each; want 11 jobs of at most 5000", jobs, most)
	}
}

// Each job plans at most one batch and chains the next; the last one starts
// the lanes. Excluded, retired and ignored Pages get no Fetch; the seed Page
// does whatever the patterns say.
func TestStartCheckPlansTheActiveNonExcludedPagesOneBatchPerJob(t *testing.T) {
	r := newPlanRig(t, 24, 10, func(pool *pgxpool.Pool, p dbgen.Project) {
		ctx := context.Background()
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE projects SET exclude_patterns = $2, max_parallel = 3 WHERE id = $1`, []any{p.ID, []string{"/p/1*", "/"}}},
			{`UPDATE pages SET state = $2 WHERE project_id = $1 AND url = 'https://shop.test/p/20'`, []any{p.ID, page.StateRetired}},
			{`UPDATE pages SET state = $2 WHERE project_id = $1 AND url = 'https://shop.test/p/21'`, []any{p.ID, page.StateIgnored}},
		} {
			if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
				t.Fatal(err)
			}
		}
	})
	jobs, most := r.plan()

	// 23 active Pages (seed + 22) in batches of 10: three jobs plan, the
	// third reaches the end and starts the lanes.
	if jobs != 3 || most > 10 {
		t.Errorf("%d StartCheck jobs, at most %d Fetches each; want 3 of at most 10", jobs, most)
	}
	rows, err := r.pool.Query(context.Background(), `SELECT pg.url FROM fetches f JOIN pages pg ON pg.id = f.page_id WHERE f.check_id = $1`, r.checkID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	want = append(want, "https://shop.test/") // the seed Page is never excluded
	for _, n := range []string{"2", "3", "4", "5", "6", "7", "8", "9", "22", "23", "24"} {
		want = append(want, "https://shop.test/p/"+n)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("planned %q\nwant %q", got, want)
	}
	c, err := r.q.GetCheck(context.Background(), r.checkID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != check.StateRunning || c.Lanes != 3 || !c.StartedAt.Valid {
		t.Errorf("Check %s with %d lanes (started %v), want running with 3", c.State, c.Lanes, c.StartedAt.Valid)
	}
	if n := r.jobs(check.LaneArgs{}.Kind()); n != 3 {
		t.Errorf("%d lane jobs, want 3", n)
	}
	if n := r.count(`SELECT count(*) FROM fetches WHERE check_id = $1 AND state = 'in_flight' AND job_id IS NOT NULL`, r.checkID); n != 3 {
		t.Errorf("%d Fetches claimed, want one per lane", n)
	}
}

// A StartCheck job that River runs twice (a retry after its commit, or the
// rescuer) finds the plan cursor moved on and does nothing.
func TestAStaleStartCheckJobPlansNothing(t *testing.T) {
	r := newPlanRig(t, 24, 10, nil)
	r.workNext(check.StartCheckArgs{}.Kind()) // begins and plans the first batch
	fetches, jobs := r.fetches(), r.jobs(check.StartCheckArgs{}.Kind())

	for _, after := range []int64{0, 1} {
		if _, err := r.startW.Work(context.Background(), t, r.tx, check.StartCheckArgs{CheckID: r.checkID, AfterPageID: after}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if r.fetches() != fetches || r.jobs(check.StartCheckArgs{}.Kind()) != jobs+2 { // +2: the two stale jobs themselves
		t.Errorf("stale jobs planned %d Fetches and chained %d jobs",
			r.fetches()-fetches, r.jobs(check.StartCheckArgs{}.Kind())-jobs-2)
	}
	if r.state() != check.StatePlanning {
		t.Errorf("Check is %s, want planning", r.state())
	}
}

// Cancelling a planning Check stops the planning; its next StartCheck job
// finalizes it, since no lane runs to do that, and the Fetches planned so far
// are skipped as cancelled.
func TestCancellingAPlanningCheckStopsPlanningAndFinalizesIt(t *testing.T) {
	r := newPlanRig(t, 24, 10, nil)
	r.workNext(check.StartCheckArgs{}.Kind())
	if r.state() != check.StatePlanning {
		t.Fatalf("Check is %s, want planning", r.state())
	}
	if n, err := r.q.RequestCancel(context.Background(), r.checkID); err != nil || n != 1 {
		t.Fatalf("cancel: %d rows, err %v", n, err)
	}
	if r.state() != check.StateCancelling {
		t.Fatalf("Check is %s, want cancelling", r.state())
	}
	planned := r.fetches()
	if !r.workNext(check.StartCheckArgs{}.Kind()) {
		t.Fatal("no StartCheck job left to notice the cancel")
	}
	if r.fetches() != planned || r.jobs(check.StartCheckArgs{}.Kind()) != 2 || r.jobs(check.LaneArgs{}.Kind()) != 0 {
		t.Errorf("planning went on after the cancel")
	}
	if !r.workNext(check.FinalizeCheckArgs{}.Kind()) {
		t.Fatal("no FinalizeCheck job")
	}
	if r.state() != check.StateCancelled {
		t.Errorf("Check is %s, want cancelled", r.state())
	}
	if n := r.count(`SELECT count(*) FROM fetches WHERE check_id = $1 AND outcome = 'skipped' AND skip_reason = 'cancelled'`, r.checkID); n != planned {
		t.Errorf("%d of %d planned Fetches skipped as cancelled", n, planned)
	}
}

// A planning Check is active: the Project cannot get a second Check, by hand
// or by schedule.
func TestAPlanningCheckIsTheProjectsActiveCheck(t *testing.T) {
	r := newPlanRig(t, 24, 10, nil)
	r.workNext(check.StartCheckArgs{}.Kind())
	got, err := check.Start(context.Background(), r.pool, r.rc, r.project.ID, check.TriggerManual)
	if !errors.Is(err, check.ErrActive) || got.ID != r.checkID {
		t.Errorf("second Start: Check %d, err %v; want %d and ErrActive", got.ID, err, r.checkID)
	}
	due, err := r.q.ListDueProjects(context.Background(), time.Now().Add(365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(due, r.project.ID) {
		t.Error("the scheduler sees a Project with a planning Check as due")
	}
}

// Job timeouts stay below River's stuck-job delay, or the rescuer would start
// a duplicate of a job that is still running (ADR 0003).
func TestJobTimeoutsStayBelowTheRescueDelay(t *testing.T) {
	start := (&startWorker{}).Timeout(nil)
	finalize := (&finalizeWorker{}).Timeout(nil)
	longestLane := laneTimeout(120) // timeout_s is at most 120
	if start <= time.Minute || start >= rescueStuckJobsAfter || finalize >= rescueStuckJobsAfter || longestLane >= rescueStuckJobsAfter {
		t.Errorf("start %s, finalize %s, longest lane %s, rescue after %s", start, finalize, longestLane, rescueStuckJobsAfter)
	}
}
