package systemtest_test

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// filteredRows returns the URLs listed by the Page pool tab under a filter.
func filteredRows(t *testing.T, s *systemtest.System, p dbgen.Project, state string) []string {
	t.Helper()
	w := s.Get("/p/" + itoa(p.ID) + "/pages?state=" + state)
	if w.Code != 200 {
		t.Fatalf("Page pool ?state=%s: status %d", state, w.Code)
	}
	var urls []string
	s.Page(w).Find("#pages tr").Each(func(i int, tr *goquery.Selection) {
		if i > 0 {
			urls = append(urls, strings.TrimSpace(tr.Find("td").Eq(0).Text()))
		}
	})
	return urls
}

func TestAManualPageNothingLinksToIsFetchedAndSurvivesManyChecks(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`home`))
	site.Set("/hidden", html(`secret`))
	p := s.AddProject("shop.test")

	// Normalised on the way in: http scheme, tracking parameter, fragment.
	if w := s.AddPage(p, "http://SHOP.test/hidden?utm_source=x#top"); w.Code != 200 {
		t.Fatalf("add Page: status %d: %s", w.Code, w.Body.String())
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE url = 'https://shop.test/hidden' AND origin = 'manual'`); n != 1 {
		t.Fatalf("manual Page not stored normalised")
	}
	for i := 0; i < 4; i++ {
		s.WaitForCheck(s.RunCheck(p))
	}
	if got := site.Hits("/hidden"); got != 4 {
		t.Errorf("/hidden fetched %d times in 4 Checks", got)
	}
	if got := filteredRows(t, s, p, "retired"); len(got) != 0 {
		t.Errorf("retired = %q", got)
	}
	if got := filteredRows(t, s, p, "active"); !sameSet(got, []string{"https://shop.test/", "https://shop.test/hidden"}) {
		t.Errorf("active = %q", got)
	}
}

func TestAddPageRejectsOtherHostsAndGarbage(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	for _, bad := range []string{"https://www.shop.test/x", "https://other.test/", "not a url", "ftp://shop.test/", ""} {
		if w := s.AddPage(p, bad); w.Code != 422 {
			t.Errorf("add %q: status %d, want 422", bad, w.Code)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM pages`); n != 1 {
		t.Errorf("%d Pages after rejected adds, want only the seed", n)
	}
}

func TestAnIgnoredPageIsNotRediscoveredOrFetchedUntilRestored(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(`a`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	a := s.PageID(p, "https://shop.test/a")

	if w := s.IgnorePage(a); w.Code != 200 {
		t.Fatalf("ignore: status %d: %s", w.Code, w.Body.String())
	}
	s.WaitForCheck(s.RunCheck(p)) // / still links to /a
	if got := site.Hits("/a"); got != 1 {
		t.Errorf("/a fetched %d times, want 1 (ignored in the second Check)", got)
	}
	if got := filteredRows(t, s, p, "ignored"); !sameSet(got, []string{"https://shop.test/a"}) {
		t.Errorf("ignored = %q", got)
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots WHERE page_id = $1`, a); n != 1 {
		t.Errorf("history dropped: %d Snapshots", n)
	}

	if w := s.RestorePage(a); w.Code != 200 {
		t.Fatalf("restore: status %d", w.Code)
	}
	s.WaitForCheck(s.RunCheck(p))
	if got := site.Hits("/a"); got != 2 {
		t.Errorf("/a fetched %d times, want 2 after Restore", got)
	}
	if got := filteredRows(t, s, p, "active"); !sameSet(got, []string{"https://shop.test/", "https://shop.test/a"}) {
		t.Errorf("active = %q", got)
	}
}

func TestTheSeedPageCannotBeIgnored(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	if w := s.IgnorePage(s.PageID(p, "https://shop.test/")); w.Code != 422 {
		t.Errorf("ignore seed: status %d, want 422", w.Code)
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE state = 'ignored'`); n != 0 {
		t.Error("seed ignored")
	}
}

func TestPagePoolFiltersByStateAndShowsColumns(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/p/x">x</a><a href="/gone">g</a>`))
	site.Set("/a", html(`a`))
	site.Set("/p/x", html(`x`))
	site.Set("/gone", html(`g`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	s.SaveRequestSettings(p, settingsForm("/p/*")) // /p/x is in the pool, now excluded
	s.IgnorePage(s.PageID(p, "https://shop.test/a"))
	site.Set("/", html(`<a href="/a">a</a><a href="/p/x">x</a>`)) // /gone orphaned
	s.WaitForCheck(s.RunCheck(p))

	for state, want := range map[string][]string{
		"active":   {"https://shop.test/", "https://shop.test/gone"},
		"orphaned": {"https://shop.test/gone"},
		"ignored":  {"https://shop.test/a"},
		"excluded": {"https://shop.test/p/x"},
		"retired":  {},
	} {
		if got := filteredRows(t, s, p, state); !sameSet(got, want) {
			t.Errorf("?state=%s = %q, want %q", state, got, want)
		}
	}
	if w := s.Get("/p/" + itoa(p.ID) + "/pages?state=bogus"); w.Code != 200 {
		t.Errorf("unknown filter: status %d", w.Code)
	}

	// Columns: state label, last status, last changed, Snapshots, first seen.
	d := s.Page(s.Get("/p/" + itoa(p.ID) + "/pages?state=orphaned"))
	row := d.Find("#pages tr").Eq(1).Find("td")
	if got := strings.TrimSpace(row.Eq(1).Text()); got != "orphaned 1/3" {
		t.Errorf("state label %q", got)
	}
	if got := strings.TrimSpace(row.Eq(3).Text()); got != "—" {
		t.Errorf("last changed %q, want — (never changed)", got)
	}
	if got := strings.TrimSpace(row.Eq(4).Text()); got != "2" {
		t.Errorf("Snapshots %q, want 2", got)
	}
	if got := strings.TrimSpace(row.Eq(5).Text()); got == "" || got == "—" {
		t.Errorf("first-seen Check %q", got)
	}
}

func TestPageHistoryListsOneRowPerCheck(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(`<title>One</title>`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	s.WaitForCheck(s.RunCheck(p)) // unchanged
	site.Set("/a", html(`<title>Two</title>`))
	s.WaitForCheck(s.RunCheck(p)) // changed
	site.Fault("/a", fakesite.Fault{Unreachable: true})
	s.WaitForCheck(s.RunCheck(p)) // fetch failure

	a := s.PageID(p, "https://shop.test/a")
	w := s.Get("/pg/" + itoa(a))
	if w.Code != 200 {
		t.Fatalf("Page history: status %d", w.Code)
	}
	d := s.Page(w)
	if got := text(d, "#page-state"); got != "active" {
		t.Errorf("state %q", got)
	}
	if got := text(d, "#page-url"); got != "https://shop.test/a" {
		t.Errorf("url %q", got)
	}
	if d.Find("#ignore").Length() != 1 || d.Find("#restore").Length() != 0 {
		t.Error("expected an Ignore button only")
	}
	var rows []string
	d.Find("#history tr").Each(func(i int, tr *goquery.Selection) {
		if i > 0 {
			rows = append(rows, strings.Join(strings.Fields(tr.Text()), " "))
		}
	})
	if len(rows) != 4 {
		t.Fatalf("%d history rows, want 4: %q", len(rows), rows)
	}
	// Newest Check first.
	for i, want := range []string{"connection", "seo", "empty", "first Snapshot"} {
		if !strings.Contains(rows[i], want) {
			t.Errorf("row %d = %q, want it to mention %q", i, rows[i], want)
		}
	}
	if n := d.Find("#history a[href^='/s/']").Length(); n != 3 {
		t.Errorf("%d Snapshot links, want 3", n)
	}
	if n := d.Find("#history a[href^='/d/']").Length(); n != 2 {
		t.Errorf("%d Diff links, want 2", n)
	}

	s.IgnorePage(a)
	d = s.Page(s.Get("/pg/" + itoa(a)))
	if text(d, "#page-state") != "ignored" || d.Find("#restore").Length() != 1 || d.Find("#ignore").Length() != 0 {
		t.Error("ignored Page should offer Restore")
	}
	if w := s.Get("/pg/999999"); w.Code != 404 {
		t.Errorf("unknown Page: status %d", w.Code)
	}
}

func TestProjectListShowsLastCheckAndReportCounts(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><title>One</title>`))
	site.Set("/a", html(`a`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", html(`<a href="/a">a</a><title>Two</title>`))
	site.Fault("/a", fakesite.Fault{Unreachable: true})
	s.WaitForCheck(s.RunCheck(p))

	d := s.Page(s.Get("/"))
	row := d.Find("tr").Eq(1)
	if got := strings.TrimSpace(row.Find(".active-pages").Text()); got != "2 active" {
		t.Errorf("active Pages %q", got)
	}
	if got := strings.TrimSpace(row.Find(".last-check").Text()); !strings.Contains(got, "finished") {
		t.Errorf("last Check %q", got)
	}
	for sel, want := range map[string]string{".r-changed": "1", ".r-critical": "0", ".r-seo": "1", ".r-failed": "1", ".r-seed-failed": "0"} {
		if got := strings.TrimSpace(row.Find(sel).Text()); got != want {
			t.Errorf("%s = %q, want %q", sel, got, want)
		}
	}
	if row.Find(".live").Length() != 0 {
		t.Error("live dot on an idle Project")
	}

	s.Exec(`UPDATE checks SET state = 'running' WHERE id = (SELECT max(id) FROM checks)`)
	if s.Page(s.Get("/")).Find("tr").Eq(1).Find(".live").Length() != 1 {
		t.Error("no live dot while a Check runs")
	}
}
