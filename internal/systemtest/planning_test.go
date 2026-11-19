package systemtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

// A Check plans its Fetches a batch at a time (ADR 0006); with batches smaller
// than the Page pool it still fetches every Page once and finishes.
func TestACheckPlannedInSeveralBatchesFetchesTheWholePool(t *testing.T) {
	s := systemtest.New(t, systemtest.PlanBatchSize(2))
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a><a href="/d">d</a><a href="/e">e</a>`))
	for _, path := range []string{"/a", "/b", "/c", "/d", "/e"} {
		site.Set(path, html(path))
	}
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p)) // discovers /a ... /e

	id := s.RunCheck(p)
	report := s.WaitForCheck(id)
	if text(report, "h1 .badge") != "finished" || text(report, ".fetched") != "6" {
		t.Errorf("state %q, fetched %q; want finished and 6", text(report, "h1 .badge"), text(report, ".fetched"))
	}
	for _, path := range []string{"/", "/a", "/b", "/c", "/d", "/e"} {
		if n := site.Hits(path); n != 2 {
			t.Errorf("%s fetched %d times over two Checks, want 2", path, n)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM river_job WHERE kind = 'start_check' AND (args->>'check_id')::bigint = $1`, id); n != 4 {
		t.Errorf("%d StartCheck jobs for 6 Pages in batches of 2, want 4 (the last finds nothing more)", n)
	}
}

// While a Check plans, its page says so and counts the Pages planned so far;
// it can be cancelled, and it counts as the Project's active Check.
func TestAPlanningCheckShowsThePagesPlannedSoFar(t *testing.T) {
	s := systemtest.New(t)
	p := testfixtures.Project(t, s.Pool, testfixtures.WithOwner(s.User), testfixtures.WithDomain("shop.test"))
	pages := []dbgen.Page{
		testfixtures.Page(t, s.Pool, p, "/", testfixtures.WithOrigin(page.OriginSeed)),
		testfixtures.Page(t, s.Pool, p, "/a"),
		testfixtures.Page(t, s.Pool, p, "/b"),
	}
	c := testfixtures.Check(t, s.Pool, p, testfixtures.WithCheckState(check.StatePlanning))
	if _, err := s.Q.PlanFetches(context.Background(), dbgen.PlanFetchesParams{
		CheckID: c.ID, ProjectID: p.ID, PageIds: []int64{pages[0].ID, pages[1].ID},
	}); err != nil {
		t.Fatal(err)
	}

	doc := s.Page(s.Get(s.CheckPath(c.ID)))
	progress := doc.Find("#progress")
	if !strings.Contains(progress.Find(".planning").Text(), "Planning") || progress.Find(".known").Text() != "2" {
		t.Errorf("progress while planning: %q", strings.TrimSpace(progress.Text()))
	}
	if progress.Find("form button").Text() != "Cancel" {
		t.Error("no Cancel button while planning")
	}
	home := s.Page(s.Get("/"))
	if got := strings.TrimSpace(home.Find(".last-check .badge").Text()); got != check.StatePlanning {
		t.Errorf("home shows the last Check as %q, want planning", got)
	}
}
