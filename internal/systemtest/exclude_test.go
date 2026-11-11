package systemtest_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func settingsForm(patterns string) url.Values {
	return url.Values{
		"schedule": {"manual"}, "user_agent": {"Bot/1"}, "timeout_s": {"30"}, "max_parallel": {"2"},
		"exclude_patterns": {patterns},
	}
}

func TestAPatternAddedBetweenChecksStopsAPageBeingFetchedAndKeepsItsHistory(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/keep">k</a><a href="/private/a">a</a><a href="/private/b">b</a>`))
	site.Set("/keep", html(`keep`))
	site.Set("/private/a", html(`a`))
	site.Set("/private/b", html(`b`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	if site.Hits("/private/a") != 1 {
		t.Fatalf("/private/a fetched %d times in the first Check", site.Hits("/private/a"))
	}

	s.SaveRequestSettings(p, settingsForm("/private/**\n"))
	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/private/a") != 1 || site.Hits("/private/b") != 1 {
		t.Errorf("excluded Pages were fetched again: a=%d b=%d", site.Hits("/private/a"), site.Hits("/private/b"))
	}
	if site.Hits("/keep") != 2 || site.Hits("/") != 2 {
		t.Errorf("other Pages: / %d, /keep %d, want 2 each", site.Hits("/"), site.Hits("/keep"))
	}
	if text(report, ".fetched") != "2" {
		t.Errorf("fetched %q, want 2", text(report, ".fetched"))
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots s JOIN pages p ON p.id = s.page_id WHERE p.url LIKE '%/private/%'`); n != 2 {
		t.Errorf("%d Snapshots of excluded Pages kept, want 2", n)
	}
	want := []string{
		"https://shop.test/|active|200", "https://shop.test/keep|active|200",
		"https://shop.test/private/a|matches exclude pattern|200", "https://shop.test/private/b|matches exclude pattern|200",
	}
	if got := poolRows(t, s, p); !sameSet(got, want) {
		t.Errorf("Page pool = %q, want %q", got, want)
	}
	pool := s.Page(s.Get("/p/" + itoa(p.ID) + "/pages"))
	pool.Find("#pages tr").Each(func(_ int, tr *goquery.Selection) {
		if strings.Contains(tr.Text(), "/private/") && tr.Find("button, form").Length() > 0 {
			t.Errorf("excluded Page row has a button: %s", tr.Text())
		}
	})

	s.SaveRequestSettings(p, settingsForm(""))
	s.WaitForCheck(s.RunCheck(p))
	if site.Hits("/private/a") != 2 || site.Hits("/private/b") != 2 {
		t.Errorf("removing the pattern didn't bring the Pages back: a=%d b=%d", site.Hits("/private/a"), site.Hits("/private/b"))
	}
	if got := poolRows(t, s, p); len(got) != 4 || strings.Contains(strings.Join(got, ","), "exclude") {
		t.Errorf("Page pool after removing the pattern = %q", got)
	}
}

func TestDiscoveryAddsNoFetchesForExcludedURLs(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/ok">ok</a><a href="/search?q=hat">s</a><a href="/tmp/x">t</a>`))
	site.Set("/ok", html(`ok`))
	site.Set("/search", html(`s`))
	site.Set("/tmp/x", html(`t`))
	p := s.AddProject("shop.test")
	s.SaveRequestSettings(p, settingsForm("/search?q=*\n/tmp/*"))

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "2" || site.Hits("/search") != 0 || site.Hits("/tmp/x") != 0 {
		t.Errorf("fetched %q, /search %d, /tmp/x %d", text(report, ".fetched"), site.Hits("/search"), site.Hits("/tmp/x"))
	}
}

func TestTheSeedPageCannotBeExcluded(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(`a`))
	p := s.AddProject("shop.test")
	s.SaveRequestSettings(p, settingsForm("**"))

	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/") != 1 || site.Hits("/a") != 0 || text(report, ".fetched") != "1" {
		t.Errorf("/ %d, /a %d, fetched %q", site.Hits("/"), site.Hits("/a"), text(report, ".fetched"))
	}
}

func TestExcludePatternsAreValidatedAndShownBackInTheForm(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	if w := s.Post("/p/"+itoa(p.ID)+"/settings", settingsForm("no-slash")); w.Code != 422 {
		t.Errorf("pattern without a leading slash: status %d, want 422", w.Code)
	}
	s.SaveRequestSettings(p, settingsForm("/a/**\n\n  /b?x=*  \n"))
	doc := s.Page(s.Get("/p/" + itoa(p.ID) + "/settings"))
	if got := doc.Find(`form#request-settings textarea[name=exclude_patterns]`).Text(); strings.TrimSpace(got) != "/a/**\n/b?x=*" {
		t.Errorf("textarea = %q", got)
	}
}
