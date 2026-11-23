package worker

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

// queryLog records every statement a pool runs.
type queryLog struct {
	mu  sync.Mutex
	sql []string
}

func (l *queryLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sql = append(l.sql, d.SQL)
	return ctx
}

func (l *queryLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// claims is how many statements claimed a pending Fetch, the only read of a
// Check's pending Fetches a lane step or planning is allowed to make. It also
// fails the test on any other statement that reads pending Fetches.
func (l *queryLog) claims(t *testing.T) int {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.sql {
		switch {
		case strings.Contains(s, "UPDATE fetches SET state = 'in_flight'"):
			n++
		case strings.Contains(s, "FROM fetches") && strings.Contains(s, "'pending'"):
			t.Errorf("a query over pending Fetches other than a claim: %s", s)
		}
	}
	return n
}

// trace makes the rig's workers run their statements through a logged pool.
func (r *planRig) trace() *queryLog {
	r.t.Helper()
	cfg := r.pool.Config()
	log := &queryLog{}
	cfg.ConnConfig.Tracer = log
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(traced.Close)
	r.d.pool = traced
	r.d.q = dbgen.New(traced)
	return log
}

// workLane works the oldest available lane job of the Check.
func (r *planRig) workLane() {
	r.t.Helper()
	ctx := context.Background()
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM river_job
		WHERE kind = 'lane' AND state = 'available' AND (args->>'check_id')::bigint = $1 ORDER BY id LIMIT 1`, r.checkID).Scan(&id)
	if err != nil {
		r.t.Fatal(err)
	}
	job, err := r.rc.JobGet(ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	cfg := &river.Config{Logger: r.d.log}
	w := rivertest.NewWorker[check.LaneArgs, pgx.Tx](r.t, riverpgxv5.New(r.pool), cfg, &laneWorker{deps: r.d})
	if _, err := w.WorkJob(ctx, r.t, r.tx, job); err != nil {
		r.t.Fatal(err)
	}
}

func (r *planRig) lanes() int32 {
	r.t.Helper()
	c, err := r.q.GetCheck(context.Background(), r.checkID)
	if err != nil {
		r.t.Fatal(err)
	}
	return c.Lanes
}

func maxParallel(n int) func(*pgxpool.Pool, dbgen.Project) {
	return func(pool *pgxpool.Pool, p dbgen.Project) {
		if _, err := pool.Exec(context.Background(), `UPDATE projects SET max_parallel = $2 WHERE id = $1`, p.ID, n); err != nil {
			panic(err)
		}
	}
}

// The bug: every lane step counted all of the Check's pending Fetches under
// the Check row lock, even with max_parallel lanes already running.
func TestALaneStepAtMaxParallelReadsOnlyItsOwnNextFetch(t *testing.T) {
	r := newPlanRig(t, 30, 100, maxParallel(3))
	r.plan()
	if r.lanes() != 3 {
		t.Fatalf("setup: %d lanes", r.lanes())
	}
	log := r.trace()
	r.workLane()
	if got := log.claims(t); got != 1 {
		t.Errorf("%d claims, want 1 (the lane's own next Fetch)", got)
	}
	if r.lanes() != 3 {
		t.Errorf("%d lanes, want 3", r.lanes())
	}
}

// Discovery can add Fetches to a Check that runs fewer lanes than it may: the
// next lane step starts the missing lanes, reading one Fetch per free lane.
func TestALaneStepBelowMaxParallelStartsTheMissingLanes(t *testing.T) {
	r := newPlanRig(t, 30, 100, maxParallel(1))
	r.plan()
	if r.lanes() != 1 {
		t.Fatalf("setup: %d lanes", r.lanes())
	}
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE checks SET settings = jsonb_set(settings, '{max_parallel}', '4') WHERE id = $1`, r.checkID); err != nil {
		t.Fatal(err)
	}
	log := r.trace()
	r.workLane()
	if got := log.claims(t); got != 4 { // its own next Fetch and three more lanes
		t.Errorf("%d claims, want 4", got)
	}
	if r.lanes() != 4 {
		t.Errorf("%d lanes, want 4", r.lanes())
	}
	if got := r.jobs("lane"); got != 5 { // the worked one and four more
		t.Errorf("%d lane jobs, want 5", got)
	}
}

// With fewer pending Fetches than free lanes, the lane step starts one lane per
// pending Fetch and stops at the first empty claim.
func TestALaneStepStartsOneLanePerPendingFetchWhenFewerThanFreeLanes(t *testing.T) {
	r := newPlanRig(t, 2, 100, maxParallel(1)) // seed + 2 Pages: 3 Fetches
	r.plan()
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE checks SET settings = jsonb_set(settings, '{max_parallel}', '10') WHERE id = $1`, r.checkID); err != nil {
		t.Fatal(err)
	}
	r.workLane() // claims the second Fetch, then the third as an extra lane
	if r.lanes() != 2 {
		t.Errorf("%d lanes, want 2", r.lanes())
	}
}

// The end of planning claims at most max_parallel Fetches, however many are
// pending, instead of counting them all.
func TestEndOfPlanningReadsAtMostMaxParallelPendingFetches(t *testing.T) {
	r := newPlanRig(t, 50, 100, maxParallel(3))
	log := r.trace()
	r.plan()
	if got := log.claims(t); got != 3 {
		t.Errorf("%d claims, want 3", got)
	}
	if r.lanes() != 3 || r.state() != check.StateRunning {
		t.Errorf("%d lanes, state %s; want 3 lanes, running", r.lanes(), r.state())
	}
}

// A Check with fewer Pages than max_parallel starts one lane per Page.
func TestACheckWithFewerPagesThanMaxParallelStartsOneLanePerPage(t *testing.T) {
	r := newPlanRig(t, 2, 100, maxParallel(10)) // seed + 2 Pages
	r.plan()
	if r.lanes() != 3 || r.jobs("lane") != 3 {
		t.Errorf("%d lanes, %d lane jobs; want 3 each", r.lanes(), r.jobs("lane"))
	}
}

// A Check with nothing to fetch finishes straight away.
func TestACheckWithNothingToFetchFinalizesAtOnce(t *testing.T) {
	r := newPlanRig(t, 0, 100, func(pool *pgxpool.Pool, p dbgen.Project) {
		if _, err := pool.Exec(context.Background(), `UPDATE pages SET state = $2 WHERE project_id = $1`, p.ID, page.StateIgnored); err != nil {
			panic(err)
		}
	})
	r.plan()
	if r.lanes() != 0 || r.jobs("lane") != 0 || r.jobs("finalize_check") != 1 {
		t.Errorf("%d lanes, %d lane jobs, %d finalize jobs", r.lanes(), r.jobs("lane"), r.jobs("finalize_check"))
	}
}
