package systemtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func crumbs(d *goquery.Document) string {
	return strings.Join(strings.Fields(d.Find(".crumbs").Text()), " ")
}

func TestTheReportOrdersItsSectionsAsTheSpecSays(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").Set("/", html(`x`))

	report := s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))

	var got []string
	report.Find("h2").Each(func(_ int, h *goquery.Selection) { got = append(got, strings.TrimSpace(h.Text())) })
	want := []string{"Changed Pages", "New Pages", "Vanished Pages", "Retired Pages", "Fetch failures"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sections = %v, want %v", got, want)
	}
}

func TestTheReportHeaderCountsChangedAndUnchanged(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><title>One</title>`))
	site.Set("/a", html(`same`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", html(`<a href="/a">a</a><title>Two</title>`))

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, "#report-header .changed") != "1" || text(report, "#report-header .unchanged") != "1" {
		t.Errorf("header changed %q unchanged %q, want 1 and 1",
			text(report, "#report-header .changed"), text(report, "#report-header .unchanged"))
	}
}

func TestEveryScreenHasTheSameBreadcrumbTrail(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<title>One</title>`))
	p := s.AddProject("shop.test")
	id := s.RunCheck(p)
	s.WaitForCheck(id)
	site.Set("/", html(`<title>Two</title>`))
	second := s.RunCheck(p)
	s.WaitForCheck(second)
	snap := latestSnapshot(t, s)
	page := s.PageID(p, "https://shop.test/")

	for _, c := range []struct{ path, want string }{
		{"/p/" + itoa(p.ID), "Projects › shop.test › Checks"},
		{"/p/" + itoa(p.ID) + "/pages", "Projects › shop.test › Page pool"},
		{"/p/" + itoa(p.ID) + "/settings", "Projects › shop.test › Settings"},
		{"/c/" + itoa(second), "Projects › shop.test › Checks › Check #" + itoa(second)},
		{"/s/" + itoa(snap), "Projects › shop.test › Checks › Check #" + itoa(second) + " › Snapshot #" + itoa(snap)},
		{"/d/" + itoa(snap), "Projects › shop.test › Checks › Check #" + itoa(second) + " › Diff /"},
		{"/pg/" + itoa(page), "Projects › shop.test › Page pool › https://shop.test/"},
	} {
		w := s.Get(c.path)
		if w.Code != 200 {
			t.Errorf("%s: status %d", c.path, w.Code)
			continue
		}
		if got := crumbs(s.Page(w)); got != c.want {
			t.Errorf("%s: breadcrumbs %q, want %q", c.path, got, c.want)
		}
	}
	// Every trail but the last crumb links back.
	d := s.Page(s.Get("/s/" + itoa(snap)))
	var hrefs []string
	d.Find(".crumbs a").Each(func(_ int, a *goquery.Selection) { hrefs = append(hrefs, a.AttrOr("href", "")) })
	want := []string{"/", "/p/" + itoa(p.ID), "/p/" + itoa(p.ID), "/c/" + itoa(second)}
	if strings.Join(hrefs, " ") != strings.Join(want, " ") {
		t.Errorf("snapshot crumb links = %v, want %v", hrefs, want)
	}
}

func TestTheChecksTabRowShowsTheReportCountsAndBadges(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a><title>One</title>`))
	site.Set("/a", html(`a`))
	site.Set("/b", html(`b`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", html(`<a href="/a">a</a><title>Two</title>`)) // SEO change, /b vanishes
	site.Fault("/a", fakesite.Fault{Unreachable: true})

	s.WaitForCheck(s.RunCheck(p))

	row := s.Page(s.Get("/p/" + itoa(p.ID))).Find("#checks tr").Eq(1) // newest first
	for sel, want := range map[string]string{
		".c-changed": "1", ".c-critical": "0", ".c-seo": "1",
		".c-new": "0", ".c-vanished": "1", ".c-retired": "0", ".c-failed": "1",
	} {
		if got := strings.TrimSpace(row.Find(sel).Text()); got != want {
			t.Errorf("%s = %q, want %q", sel, got, want)
		}
	}
	if row.Find(".badge.seed-failed").Length() != 0 {
		t.Error("seed failed badge on a Check whose seed succeeded")
	}
	if row.Find("td").Eq(3).Text() == "" || strings.TrimSpace(row.Find(".c-trigger").Text()) != "manual" {
		t.Errorf("trigger cell %q", row.Find(".c-trigger").Text())
	}

	site.Fault("/", fakesite.Fault{Unreachable: true})
	s.WaitForCheck(s.RunCheck(p))
	row = s.Page(s.Get("/p/" + itoa(p.ID))).Find("#checks tr").Eq(1)
	if row.Find(".badge.seed-failed").Length() != 1 {
		t.Error("no seed failed badge on a Check whose seed failed")
	}
}

func TestASkippedSeedCountsAsSeedFailedOnTheChecksTabAndProjectList(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(``))
	site.Set("/robots.txt", robotsTxt("User-agent: *\nDisallow: /"))
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")

	s.WaitForCheck(s.RunCheck(p))

	if s.Page(s.Get("/p/"+itoa(p.ID))).Find("#checks .badge.seed-failed").Length() != 1 {
		t.Error("no seed failed badge for a skipped seed on the Checks tab")
	}
	if got := text(s.Page(s.Get("/")), ".r-seed-failed"); got != "1" {
		t.Errorf("project list seed failed = %q, want 1", got)
	}
}

func TestLiveProgressShowsNewlyDiscoveredPages(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a>`))
	site.Set("/a", html(`a`))
	site.Set("/b", html(`b`))
	site.Fault("/a", fakesite.Fault{Delay: 2 * time.Second})
	p := s.AddProject("shop.test")
	s.Exec(`UPDATE projects SET max_parallel = 1 WHERE id = $1`, p.ID)

	id := s.RunCheck(p)
	s.WaitFor(func() bool { return site.Hits("/a") > 0 })

	got := text(s.Page(s.GetHTMX("/c/"+itoa(id)+"/progress")), ".discovered")
	if got != "2" {
		t.Errorf("newly discovered Pages in progress = %q, want 2", got)
	}
	s.WaitForCheck(id)
}

func TestAnIgnoredOrExcludedPageIsLeftOutOfTheChangedList(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`))
	for _, path := range []string{"/a", "/b", "/c"} {
		site.Set(path, html(path+" one"))
	}
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	for _, path := range []string{"/a", "/b", "/c"} {
		site.Set(path, html(path+" two"))
	}
	id := s.RunCheck(p)
	s.WaitForCheck(id)
	s.IgnorePage(s.PageID(p, "https://shop.test/a"))
	s.SaveRequestSettings(p, settingsForm("/b\n"))

	report := s.Page(s.Get("/c/" + itoa(id)))

	if got := changedRows(report); len(got) != 1 || !strings.HasPrefix(got[0], "https://shop.test/c|") {
		t.Errorf("changed rows = %v, want only /c", got)
	}
	if text(report, "#report-header .changed") != "1" || text(report, "#report-header .unchanged") != "1" {
		t.Errorf("header changed %q unchanged %q, want 1 (/c) and 1 (/)",
			text(report, "#report-header .changed"), text(report, "#report-header .unchanged"))
	}
}
