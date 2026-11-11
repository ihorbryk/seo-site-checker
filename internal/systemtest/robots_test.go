package systemtest_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// respectRobots turns "respect robots.txt" on through the Settings tab.
func respectRobots(s *systemtest.System, p dbgen.Project, userAgent string) {
	s.T.Helper()
	s.SaveRequestSettings(p, url.Values{
		"schedule": {"manual"}, "user_agent": {userAgent}, "timeout_s": {"30"}, "max_parallel": {"2"},
		"respect_robots": {"on"},
	})
}

func robotsTxt(body string) fakesite.Response {
	return fakesite.Response{Header: http.Header{"Content-Type": {"text/plain"}}, Body: body}
}

// skippedList returns "url|reason" for each row of the filtered skipped list.
func skippedList(t *testing.T, s *systemtest.System, checkID int64) []string {
	t.Helper()
	w := s.Get("/c/" + itoa(checkID) + "?show=skipped")
	if w.Code != 200 {
		t.Fatalf("skipped list: status %d", w.Code)
	}
	var rows []string
	s.Page(w).Find("#skipped-list tr").Each(func(i int, tr *goquery.Selection) {
		if i == 0 {
			return
		}
		td := tr.Find("td")
		rows = append(rows, strings.TrimSpace(td.Eq(0).Text())+"|"+strings.TrimSpace(td.Eq(1).Text()))
	})
	return rows
}

func linkedSite(s *systemtest.System) *fakesite.Site {
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/open">o</a><a href="/private/x">p</a>`))
	site.Set("/open", html(`open`))
	site.Set("/private/x", html(`<a href="/hidden-by-skip">h</a>`))
	return site
}

func TestADisallowedPageIsSkippedWithoutARequestAndKeepsItsLinksOut(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", robotsTxt("User-agent: *\nDisallow: /private"))
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")

	checkID := s.RunCheck(p)
	report := s.WaitForCheck(checkID)

	if site.Hits("/private/x") != 0 {
		t.Error("a disallowed Page was requested")
	}
	if site.Hits("/robots.txt") != 1 {
		t.Errorf("robots.txt fetched %d times, want once per Check", site.Hits("/robots.txt"))
	}
	if text(report, ".fetched") != "2" || text(report, ".skipped") != "1" {
		t.Errorf("fetched %q skipped %q, want 2 and 1", text(report, ".fetched"), text(report, ".skipped"))
	}
	if href := report.Find(".skipped a").AttrOr("href", ""); href != "/c/"+itoa(checkID)+"?show=skipped" {
		t.Errorf("skipped count links to %q", href)
	}
	if got := skippedList(t, s, checkID); !sameSet(got, []string{"https://shop.test/private/x|robots_disallowed"}) {
		t.Errorf("skipped list = %q", got)
	}
	if report.Find("#robots-banner").Length() != 0 {
		t.Error("unreachable banner on a readable robots.txt")
	}
	// The skipped Page stays in the pool, is not orphaned (the seed links to it), and its links were not read.
	want := []string{"https://shop.test/|active|200", "https://shop.test/open|active|200", "https://shop.test/private/x|active|—"}
	if got := poolRows(t, s, p); !sameSet(got, want) {
		t.Errorf("Page pool = %q, want %q", got, want)
	}
	if n := count(t, s, `SELECT count(*) FROM page_events WHERE kind IN ('vanished', 'retired')`); n != 0 {
		t.Errorf("%d orphan events", n)
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots`); n != 2 {
		t.Errorf("%d Snapshots, want 2", n)
	}
}

func TestRobotsTxtIsStoredOnTheCheck(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", robotsTxt("User-agent: *\nDisallow: /private"))
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")
	s.WaitForCheck(s.RunCheck(p))

	var status int
	var body string
	var fetched bool
	if err := s.Pool.QueryRow(t.Context(), `SELECT robots_status, convert_from(robots_body, 'UTF8'), robots_fetched_at IS NOT NULL FROM checks`).Scan(&status, &body, &fetched); err != nil {
		t.Fatal(err)
	}
	if status != 200 || body != "User-agent: *\nDisallow: /private" || !fetched {
		t.Errorf("stored robots = %d %q fetched=%v", status, body, fetched)
	}
}

func TestRobotsTxtIsNotFetchedWhenTheSettingIsOff(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", robotsTxt("User-agent: *\nDisallow: /"))
	p := s.AddProject("shop.test")
	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/robots.txt") != 0 || site.Hits("/private/x") != 1 {
		t.Errorf("robots.txt hits %d, /private/x hits %d", site.Hits("/robots.txt"), site.Hits("/private/x"))
	}
	if text(report, ".skipped") != "0" {
		t.Errorf("skipped %q", text(report, ".skipped"))
	}
	if n := count(t, s, `SELECT count(*) FROM checks WHERE robots_status IS NOT NULL OR robots_fetched_at IS NOT NULL`); n != 0 {
		t.Error("robots columns set though the setting is off")
	}
}

func TestALongerAllowRuleBeatsAShorterDisallow(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", robotsTxt("User-agent: *\nDisallow: /private\nAllow: /private/x"))
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")
	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/private/x") != 1 || text(report, ".skipped") != "0" {
		t.Errorf("hits %d skipped %q", site.Hits("/private/x"), text(report, ".skipped"))
	}
}

func TestA404RobotsTxtAllowsEverything(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s) // no robots.txt: the fake site answers 404
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")
	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/private/x") != 1 || text(report, ".skipped") != "0" || report.Find("#robots-banner").Length() != 0 {
		t.Errorf("hits %d skipped %q banner %d", site.Hits("/private/x"), text(report, ".skipped"), report.Find("#robots-banner").Length())
	}
}

func TestA503RobotsTxtSkipsEveryFetchWithABanner(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", fakesite.Response{Status: 503, Body: "down"})
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")

	checkID := s.RunCheck(p)
	report := s.WaitForCheck(checkID)

	if site.Hits("/") != 0 {
		t.Error("a Page was requested though robots.txt was unreachable")
	}
	if text(report, ".skipped") != "1" || text(report, ".fetched") != "0" {
		t.Errorf("skipped %q fetched %q", text(report, ".skipped"), text(report, ".fetched"))
	}
	if report.Find("#robots-banner").Length() != 1 {
		t.Error("no robots.txt unreachable banner")
	}
	if got := skippedList(t, s, checkID); !sameSet(got, []string{"https://shop.test/|robots_unreachable"}) {
		t.Errorf("skipped list = %q", got)
	}
	// Nothing was seen, so no Page is orphaned and the banner for it shows.
	if report.Find("#orphan-banner").Length() != 1 {
		t.Error("orphan-counting banner missing")
	}
}

func TestAnUnreachableRobotsTxtSkipsEveryFetch(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Fault("/robots.txt", fakesite.Fault{Unreachable: true})
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")
	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/") != 0 || report.Find("#robots-banner").Length() != 1 || text(report, ".skipped") != "1" {
		t.Errorf("seed hits %d, banner %d, skipped %q", site.Hits("/"), report.Find("#robots-banner").Length(), text(report, ".skipped"))
	}
	var kind string
	if err := s.Pool.QueryRow(t.Context(), `SELECT robots_failure_kind FROM checks`).Scan(&kind); err != nil || kind != "connection" {
		t.Errorf("failure kind %q, err %v", kind, err)
	}
}

func TestSkippedPagesStillLinkedAreNotOrphanedAcrossChecks(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p)) // robots off: everything is fetched and linked
	site.Set("/robots.txt", robotsTxt("User-agent: *\nDisallow: /private"))
	respectRobots(s, p, "SEOSiteChecker/0.1")

	for i := 0; i < 4; i++ {
		s.WaitForCheck(s.RunCheck(p))
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE state = 'retired' OR orphan_streak > 0`); n != 0 {
		t.Errorf("%d Pages orphaned or retired though still linked", n)
	}
}

func TestARobotsTxtRedirectIsFollowed(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", fakesite.Response{Status: 301, Header: http.Header{"Location": {"/real-robots.txt"}}})
	site.Set("/real-robots.txt", robotsTxt("User-agent: *\nDisallow: /private"))
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")
	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/private/x") != 0 || text(report, ".skipped") != "1" {
		t.Errorf("private hits %d, skipped %q", site.Hits("/private/x"), text(report, ".skipped"))
	}
}

func TestAnEndlessRobotsTxtRedirectAllowsEverything(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", fakesite.Response{Status: 302, Header: http.Header{"Location": {"/robots.txt"}}})
	p := s.AddProject("shop.test")
	respectRobots(s, p, "SEOSiteChecker/0.1")
	report := s.WaitForCheck(s.RunCheck(p))

	if got := site.Hits("/robots.txt"); got != 6 {
		t.Errorf("robots.txt requested %d times, want 1 + 5 redirects", got)
	}
	if text(report, ".skipped") != "0" || report.Find("#robots-banner").Length() != 0 {
		t.Error("redirect loop should be treated like a 4xx")
	}
}

func TestRobotsTxtIsFetchedWithTheChecksUserAgentAndHeaders(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	p := s.AddProject("shop.test")
	s.SaveRequestSettings(p, url.Values{
		"schedule": {"manual"}, "user_agent": {"Googlebot/2.1"}, "timeout_s": {"30"}, "max_parallel": {"2"},
		"respect_robots": {"on"}, "header_name": {"X-Env"}, "header_value": {"stage"},
	})
	s.WaitForCheck(s.RunCheck(p))

	got := site.Requests("/robots.txt")
	if len(got) != 1 || got[0].Get("User-Agent") != "Googlebot/2.1" || got[0].Get("X-Env") != "stage" {
		t.Errorf("robots.txt request = %v", got)
	}
}

func TestAGooglebotGroupAppliesToAGooglebotUserAgent(t *testing.T) {
	s := systemtest.New(t)
	site := linkedSite(s)
	site.Set("/robots.txt", robotsTxt("User-agent: *\nDisallow: /open\n\nUser-agent: googlebot\nDisallow: /private"))
	p := s.AddProject("shop.test")
	respectRobots(s, p, "Googlebot/2.1 (+http://www.google.com/bot.html)")
	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/open") != 1 || site.Hits("/private/x") != 0 || text(report, ".skipped") != "1" {
		t.Errorf("open hits %d, private hits %d, skipped %q", site.Hits("/open"), site.Hits("/private/x"), text(report, ".skipped"))
	}
}
