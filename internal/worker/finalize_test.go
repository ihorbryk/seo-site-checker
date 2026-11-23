package worker

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

// finalizeScenario is a Project's second Check, its lanes ended, waiting for
// FinalizeCheck. Its Pages, in id order, and what happened to them:
//
//	/   seed, Snapshot; links to /a, /e, /f, /h
//	/a  Snapshot, orphan streak 1; linked by /       -> streak 0
//	/b  Fetch failure; linked by /a                  -> not counted
//	/c  Snapshot; linked by nobody                   -> vanished (streak 1)
//	/d  Snapshot, streak 2; linked by nobody         -> retired (streak 3)
//	/e  skipped by robots.txt; linked by /           -> its first-Check
//	    Snapshot still links to /g
//	/f  Snapshot, its first ever; linked by /        -> new
//	/g  Snapshot, streak 1; linked only by /e's
//	    first-Check Snapshot                         -> streak 0
//	/h  Snapshot, brought back by discovery in this
//	    Check; linked by /                           -> reactivated
//
// The Check's Diffs by tier: one Page each in critical, seo, headers and body,
// and three empty ones (scenarioTiers).
type finalizeScenario struct {
	*planRig
	pages map[string]dbgen.Page
}

var scenarioTiers = map[string]string{
	"/": "critical", "/a": "seo", "/c": "body", "/d": "empty", "/f": "empty", "/g": "headers", "/h": "empty",
}

func newFinalizeScenario(t *testing.T, batch int) *finalizeScenario {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	p := testfixtures.Project(t, pool, testfixtures.WithDomain("shop.test"))
	s := &finalizeScenario{pages: map[string]dbgen.Page{}}
	for _, path := range []string{"/", "/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h"} {
		origin := page.OriginDiscovered
		if path == "/" {
			origin = page.OriginSeed
		}
		s.pages[path] = testfixtures.Page(t, pool, p, path, testfixtures.WithOrigin(origin))
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	link := func(snapshotID int64, targets ...string) {
		t.Helper()
		for _, path := range targets {
			exec(`INSERT INTO links (snapshot_id, target_page_id, source) VALUES ($1, $2, 'a')`, snapshotID, s.pages[path].ID)
		}
	}

	first := testfixtures.Check(t, pool, p, testfixtures.WithCheckState(check.StateFinished))
	for _, path := range []string{"/", "/a", "/b", "/c", "/d", "/e", "/g", "/h"} {
		snap := testfixtures.Snapshot(t, pool, first, s.pages[path])
		if path == "/e" {
			link(snap.ID, "/g")
		}
	}

	c := testfixtures.Check(t, pool, p, testfixtures.WithCheckState(check.StateRunning))
	exec(`UPDATE pages SET orphan_streak = 1 WHERE id = ANY($1)`, []int64{s.pages["/a"].ID, s.pages["/g"].ID})
	exec(`UPDATE pages SET orphan_streak = 2 WHERE id = $1`, s.pages["/d"].ID)
	exec(`UPDATE pages SET reactivated_check_id = $2 WHERE id = $1`, s.pages["/h"].ID, c.ID)
	for _, path := range []string{"/", "/a", "/c", "/d", "/f", "/g", "/h"} {
		snap := testfixtures.Snapshot(t, pool, c, s.pages[path])
		if tier, ok := scenarioTiers[path]; ok {
			exec(`INSERT INTO diffs (snapshot_id, check_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, tier)
				VALUES ($1, $3, $1, false, false, false, false, $2)`, snap.ID, tier, c.ID)
		}
		switch path {
		case "/":
			link(snap.ID, "/a", "/e", "/f", "/h")
		case "/a":
			link(snap.ID, "/b")
		}
	}
	q := dbgen.New(pool)
	if _, err := q.PlanFetches(ctx, dbgen.PlanFetchesParams{CheckID: c.ID, ProjectID: p.ID, PageIds: []int64{s.pages["/b"].ID, s.pages["/e"].ID}}); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE fetches SET state = 'done', outcome = 'failure', failure_kind = 'timeout' WHERE check_id = $1 AND page_id = $2`, c.ID, s.pages["/b"].ID)
	exec(`UPDATE fetches SET state = 'done', outcome = 'skipped', skip_reason = 'robots_disallowed' WHERE check_id = $1 AND page_id = $2`, c.ID, s.pages["/e"].ID)

	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Insert(ctx, check.FinalizeCheckArgs{CheckID: c.ID}, nil); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &deps{pool: pool, q: q, client: siteClient(t), log: log, finalizeBatchSize: batch}
	driver := riverpgxv5.New(pool)
	cfg := &river.Config{Logger: log}
	s.planRig = &planRig{
		t: t, pool: pool, q: q, rc: rc, tx: newPoolTx(t, pool), project: p, checkID: c.ID,
		startW: rivertest.NewWorker[check.StartCheckArgs, pgx.Tx](t, driver, cfg, &startWorker{deps: d}),
		finW:   rivertest.NewWorker[check.FinalizeCheckArgs, pgx.Tx](t, driver, cfg, &finalizeWorker{deps: d}),
	}
	return s
}

// finalize works FinalizeCheck jobs until none is left and returns how many ran.
func (s *finalizeScenario) finalize() int {
	n := 0
	for s.workNext(check.FinalizeCheckArgs{}.Kind()) {
		n++
	}
	return n
}

// events returns the Check's page events as "kind path".
func (s *finalizeScenario) events() []string {
	s.t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT e.kind || ' ' || substr(pg.url, length('https://shop.test') + 1)
		FROM page_events e JOIN pages pg ON pg.id = e.page_id WHERE e.check_id = $1`, s.checkID)
	if err != nil {
		s.t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		s.t.Fatal(err)
	}
	slices.Sort(got)
	return got
}

// pageStates returns "state streak" for each Page by path.
func (s *finalizeScenario) pageStates() map[string]string {
	s.t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT substr(url, length('https://shop.test') + 1), state || ' ' || orphan_streak
		FROM pages WHERE project_id = $1`, s.project.ID)
	if err != nil {
		s.t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var path, st string
		if err := rows.Scan(&path, &st); err != nil {
			s.t.Fatal(err)
		}
		got[path] = st
	}
	if err := rows.Err(); err != nil {
		s.t.Fatal(err)
	}
	return got
}

// The scenario's Report, worked out by hand, must come out the same whether
// FinalizeCheck takes it in one batch or in batches of two Pages.
func TestFinalizingInBatchesRecordsTheSameEventsStreaksAndCounts(t *testing.T) {
	for _, batch := range []int{1, 3, 100} {
		s := newFinalizeScenario(t, batch)
		s.finalize()

		c, err := s.q.GetCheck(context.Background(), s.checkID)
		if err != nil {
			t.Fatal(err)
		}
		if c.State != check.StateFinished || !c.FinishedAt.Valid || c.OrphanCountingSkipped {
			t.Errorf("batch %d: Check %s (finished %v, orphan counting skipped %v), want finished", batch, c.State, c.FinishedAt.Valid, c.OrphanCountingSkipped)
		}
		counts := []int32{c.CountSnapshots, c.CountFailures, c.CountSkipped, c.CountNew, c.CountVanished, c.CountRetired}
		if want := []int32{7, 1, 1, 2, 1, 1}; !slices.Equal(counts, want) {
			t.Errorf("batch %d: snapshots, failures, skipped, new, vanished, retired = %v, want %v", batch, counts, want)
		}
		// The Report reads its sections against the Pages FinalizeCheck walked
		// (ADR 0010), whether or not each was fetched.
		if c.CountPages != 9 {
			t.Errorf("batch %d: count_pages = %d, want 9", batch, c.CountPages)
		}
		if got, want := s.events(), []string{"new /f", "reactivated /h", "retired /d", "vanished /c"}; !slices.Equal(got, want) {
			t.Errorf("batch %d: events %q, want %q", batch, got, want)
		}
		want := map[string]string{
			"/": "active 0", "/a": "active 0", "/b": "active 0", "/c": "active 1", "/d": "retired 3",
			"/e": "active 0", "/f": "active 0", "/g": "active 0", "/h": "active 0",
		}
		got := s.pageStates()
		for path, w := range want {
			if got[path] != w {
				t.Errorf("batch %d: %s is %q, want %q", batch, path, got[path], w)
			}
		}
	}
}

// Each FinalizeCheck job finalizes one batch and chains the next; until the
// last one ends it the Check stays running, and it can no longer be cancelled.
func TestEachFinalizeCheckJobFinalizesOneBatchAndTheCheckCannotBeCancelledMeanwhile(t *testing.T) {
	s := newFinalizeScenario(t, 2)
	if !s.workNext(check.FinalizeCheckArgs{}.Kind()) {
		t.Fatal("no FinalizeCheck job")
	}
	c, err := s.q.GetCheck(context.Background(), s.checkID)
	if err != nil {
		t.Fatal(err)
	}
	// The Check's Fetches by page id: /, /a | /b, /c | ... so the first batch
	// ends at /a.
	if c.State != check.StateRunning || c.FinalizeCursor.Int64 != s.pages["/a"].ID {
		t.Errorf("after one job: Check %s, cursor %v; want running at /a (%d)", c.State, c.FinalizeCursor, s.pages["/a"].ID)
	}
	if n, err := s.q.RequestCancel(context.Background(), s.checkID); err != nil || n != 0 {
		t.Errorf("cancel while finalizing changed %d Checks (err %v), want none", n, err)
	}
	// Nine Fetches in batches of two: four full batches, then the last.
	if jobs := 1 + s.finalize(); jobs != 5 {
		t.Errorf("%d FinalizeCheck jobs, want 5", jobs)
	}
	if st := s.state(); st != check.StateFinished {
		t.Errorf("Check is %s, want finished", st)
	}
}

// A FinalizeCheck job that River runs twice (a retry after its commit, or the
// rescuer) finds the cursor moved on and does nothing.
func TestAStaleFinalizeCheckJobDoesNothing(t *testing.T) {
	s := newFinalizeScenario(t, 2)
	s.workNext(check.FinalizeCheckArgs{}.Kind())
	before := s.events()
	jobs := s.jobs(check.FinalizeCheckArgs{}.Kind())
	for _, after := range []int64{0, s.pages["/"].ID} {
		if _, err := s.finW.Work(context.Background(), t, s.tx, check.FinalizeCheckArgs{CheckID: s.checkID, AfterPageID: after}, nil); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.q.GetCheck(context.Background(), s.checkID)
	if err != nil {
		t.Fatal(err)
	}
	if c.FinalizeCursor.Int64 != s.pages["/a"].ID || !slices.Equal(s.events(), before) || s.jobs(check.FinalizeCheckArgs{}.Kind()) != jobs+2 {
		t.Errorf("stale jobs moved the cursor to %d, events %q -> %q, or chained jobs", c.FinalizeCursor.Int64, before, s.events())
	}
}

// A cancelled Check is finalized in batches too: every batch skips its pending
// Fetches, and orphans are left alone.
func TestACancelledCheckIsFinalizedInBatchesSkippingEveryPendingFetch(t *testing.T) {
	s := newFinalizeScenario(t, 2)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE fetches SET state = 'pending', outcome = NULL, skip_reason = NULL
		WHERE check_id = $1 AND page_id = ANY($2)`, s.checkID, []int64{s.pages["/e"].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM snapshots WHERE check_id = $1 AND page_id = ANY($2)`,
		s.checkID, []int64{s.pages["/c"].ID, s.pages["/h"].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE fetches SET state = 'pending', outcome = NULL
		WHERE check_id = $1 AND page_id = ANY($2)`, s.checkID, []int64{s.pages["/c"].ID, s.pages["/h"].ID}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.q.RequestCancel(ctx, s.checkID); err != nil || n != 1 {
		t.Fatalf("cancel: %d, %v", n, err)
	}
	s.finalize()

	c, err := s.q.GetCheck(ctx, s.checkID)
	if err != nil {
		t.Fatal(err)
	}
	counts := []int32{c.CountSnapshots, c.CountFailures, c.CountSkipped, c.CountNew, c.CountVanished, c.CountRetired}
	// /c, /e and /h were pending: skipped. /h still counts as reactivated.
	if want := []int32{5, 1, 3, 2, 0, 0}; c.State != check.StateCancelled || !slices.Equal(counts, want) {
		t.Errorf("Check %s with counts %v, want cancelled with %v", c.State, counts, want)
	}
	if n := s.count(`SELECT count(*) FROM fetches WHERE check_id = $1 AND state = 'pending'`, s.checkID); n != 0 {
		t.Errorf("%d Fetches still pending", n)
	}
	if got := s.pageStates(); got["/a"] != "active 1" || got["/d"] != "active 2" {
		t.Errorf("orphan streaks changed in a cancelled Check: /a %q, /d %q", got["/a"], got["/d"])
	}
}

// The Diff tiers a Report stores are the Check's Diffs counted directly,
// however many batches FinalizeCheck takes. Every Diff counts, including that
// of a Page ignored while the Check ran (ADR 0008).
func TestFinalizingInBatchesStoresTheCheckDiffTiersAndSeedFailure(t *testing.T) {
	for _, batch := range []int{1, 2, 100} {
		for _, seed := range []struct {
			name, set string
			failed    bool
		}{
			{"snapshot", ``, false},
			{"failure", `outcome = 'failure', failure_kind = 'timeout'`, true},
			{"skipped by robots", `outcome = 'skipped', skip_reason = 'robots_disallowed'`, true},
			{"skipped by cancel", `outcome = 'skipped', skip_reason = 'cancelled'`, false},
		} {
			s := newFinalizeScenario(t, batch)
			ctx := context.Background()
			if _, err := s.pool.Exec(ctx, `UPDATE pages SET state = 'ignored' WHERE id = $1`, s.pages["/c"].ID); err != nil {
				t.Fatal(err)
			}
			if seed.set != "" {
				if _, err := s.pool.Exec(ctx, `UPDATE fetches SET `+seed.set+` WHERE check_id = $1 AND page_id = $2`, s.checkID, s.pages["/"].ID); err != nil {
					t.Fatal(err)
				}
			}
			s.finalize()
			c, err := s.q.GetCheck(ctx, s.checkID)
			if err != nil {
				t.Fatal(err)
			}
			got := []int32{c.CountDiffCritical, c.CountDiffSeo, c.CountDiffHeaders, c.CountDiffBody, c.CountDiffEmpty}
			if want := []int32{1, 1, 1, 1, 3}; !slices.Equal(got, want) {
				t.Errorf("batch %d, seed %s: critical, seo, headers, body, empty = %v, want %v", batch, seed.name, got, want)
			}
			if c.SeedFailed != seed.failed {
				t.Errorf("batch %d, seed %s: seed_failed = %v, want %v", batch, seed.name, c.SeedFailed, seed.failed)
			}
		}
	}
}
