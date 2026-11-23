package worker

import (
	"context"
	"net/http"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// progressSite links "/" to a reachable page, an unreachable one (a Fetch
// failure) and one robots.txt disallows (a skip), and the reachable page to
// one more page nobody planned (URL discovery mid-Check).
func progressSite(t *testing.T) *http.Client {
	site := fakesite.New(t, "shop.test")
	site.Set("/", fakesite.Response{Body: `<html><a href="/ok">a</a><a href="/down">b</a><a href="/c">c</a></html>`})
	site.Set("/ok", fakesite.Response{Body: `<html><a href="/deeper">d</a></html>`})
	site.Set("/deeper", fakesite.Response{Body: "<html></html>"})
	site.Fault("/down", fakesite.Fault{Unreachable: true})
	n := fakesite.NewNetwork()
	n.Add(site)
	return n.Client()
}

// requireProgressMatchesRows fails unless the Check's running counters equal
// what counting its Fetches and Pages gives.
func (r *rig) requireProgressMatchesRows() {
	r.t.Helper()
	var got, want [4]int64
	err := r.pool.QueryRow(context.Background(), `
		SELECT c.progress_known, c.progress_fetched, c.progress_failed, c.progress_discovered,
			(SELECT count(*) FROM fetches f WHERE f.check_id = c.id),
			(SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.state = 'done'),
			(SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.outcome = 'failure'),
			(SELECT count(*) FROM pages p WHERE p.project_id = c.project_id AND p.origin = 'discovered' AND p.created_at >= c.started_at)
		FROM checks c WHERE c.id = $1`, r.checkID).
		Scan(&got[0], &got[1], &got[2], &got[3], &want[0], &want[1], &want[2], &want[3])
	if err != nil {
		r.t.Fatal(err)
	}
	if got != want {
		r.t.Fatalf("counters known/fetched/failed/discovered = %v, rows say %v", got, want)
	}
}

// workLanes works the Check's lane jobs one at a time until none is left,
// calling each after every step.
func (r *rig) workLanes(each func()) {
	r.t.Helper()
	ctx := context.Background()
	rc, err := check.NewInsertClient(r.pool)
	if err != nil {
		r.t.Fatal(err)
	}
	for {
		var id int64
		err := r.pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind = 'lane' AND state = 'available'
			AND (args->>'check_id')::bigint = $1 ORDER BY id LIMIT 1`, r.checkID).Scan(&id)
		if err == pgx.ErrNoRows {
			return
		}
		if err != nil {
			r.t.Fatal(err)
		}
		job, err := rc.JobGet(ctx, id)
		if err != nil {
			r.t.Fatal(err)
		}
		if res, err := r.laneW.WorkJob(ctx, r.t, r.tx, job); err != nil || res.EventKind != river.EventKindJobCompleted {
			r.t.Fatalf("lane: %v, %v", res, err)
		}
		each()
	}
}

func (r *rig) disallowViaRobots(path string) {
	r.t.Helper()
	_, err := r.pool.Exec(context.Background(), `UPDATE checks SET robots_status = 200, robots_body = $2,
		settings = jsonb_set(settings, '{respect_robots}', 'true') WHERE id = $1`, r.checkID, "User-agent: *\nDisallow: "+path+"\n")
	if err != nil {
		r.t.Fatal(err)
	}
}

// Through planning, URL discovery, a failure and a robots.txt skip, the
// counters equal what counting the rows gives after every step.
func TestProgressCountersEqualTheRowsAtEveryStep(t *testing.T) {
	r := newRig(t, progressSite(t), "/")
	r.requireProgressMatchesRows()
	r.disallowViaRobots("/c")
	r.workLanes(r.requireProgressMatchesRows)

	var known, fetched, failed, discovered int64
	err := r.pool.QueryRow(context.Background(), `SELECT progress_known, progress_fetched, progress_failed, progress_discovered
		FROM checks WHERE id = $1`, r.checkID).Scan(&known, &fetched, &failed, &discovered)
	if err != nil {
		t.Fatal(err)
	}
	if known != 5 || fetched != 5 || failed != 1 || discovered != 4 {
		t.Errorf("known %d fetched %d failed %d discovered %d; want 5 5 1 4", known, fetched, failed, discovered)
	}
}

// A cancelled Check stops fetching: its counters still equal the rows.
func TestProgressCountersEqualTheRowsOnACancelledCheck(t *testing.T) {
	r := newRig(t, progressSite(t), "/")
	steps := 0
	r.workLanes(func() {
		r.requireProgressMatchesRows()
		if steps++; steps == 1 {
			if _, err := r.q.RequestCancel(context.Background(), r.checkID); err != nil {
				t.Fatal(err)
			}
		}
	})
	r.requireProgressMatchesRows()
}

// FinalizeCheck skips the pending Fetches of a cancelling Check: its counters
// count them in the same transaction, so they never lag the rows.
func TestProgressCountersEqualTheRowsAfterFinalizingACancelledCheck(t *testing.T) {
	r := newRig(t, progressSite(t), "/")
	steps := 0
	r.workLanes(func() {
		if steps++; steps == 1 {
			if _, err := r.q.RequestCancel(context.Background(), r.checkID); err != nil {
				t.Fatal(err)
			}
		}
	})
	var pending int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM fetches WHERE check_id = $1 AND state = 'pending'`, r.checkID).Scan(&pending); err != nil || pending == 0 {
		t.Fatalf("%d pending Fetches before FinalizeCheck (err %v); the test needs some", pending, err)
	}
	r.finalize()
	r.requireProgressMatchesRows()
}

// A lane whose attempt failed before it committed leaves nothing counted; its
// retry counts the Fetch once.
func TestARetriedLaneCountsItsFetchOnce(t *testing.T) {
	r := newRig(t, siteClient(t), "/")
	r.breakSettings()
	res, err := r.workLane()
	if err == nil || res.EventKind != river.EventKindJobFailed {
		t.Fatalf("first attempt: %v, %v; want a job error", res, err)
	}
	r.requireProgressMatchesRows()
	r.lane = res.Job
	r.setAttempt(r.lane.MaxAttempts - 1)
	if res, err := r.workLane(); err != nil || res.EventKind != river.EventKindJobCompleted {
		t.Fatalf("last attempt: %v, %v", res, err)
	}
	r.requireProgressMatchesRows()
	var fetched, failed int64
	if err := r.pool.QueryRow(context.Background(), `SELECT progress_fetched, progress_failed FROM checks WHERE id = $1`, r.checkID).Scan(&fetched, &failed); err != nil {
		t.Fatal(err)
	}
	if fetched != 1 || failed != 1 {
		t.Errorf("fetched %d failed %d, want 1 and 1", fetched, failed)
	}
}
