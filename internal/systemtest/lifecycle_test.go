package systemtest_test

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// section returns the Page URLs listed in a Report section (a table by id).
func section(d *goquery.Document, id string) []string {
	var urls []string
	d.Find("#" + id + " td.mono").Each(func(_ int, td *goquery.Selection) {
		urls = append(urls, strings.TrimSpace(td.Text()))
	})
	return urls
}

func TestAPageDroppedFromLinksVanishesThenRetiresAndIsNotFetched(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a>`))
	site.Set("/a", html(``))
	site.Set("/b", html(``))
	p := s.AddProject("shop.test")

	first := s.WaitForCheck(s.RunCheck(p))
	if got := section(first, "vanished"); len(got) != 0 {
		t.Errorf("first Check vanished %q", got)
	}
	if got := section(first, "new"); !sameSet(got, []string{"https://shop.test/a", "https://shop.test/b"}) {
		t.Errorf("first Check new Pages = %q", got)
	}

	site.Set("/", html(`<a href="/a">a</a>`)) // /b is no longer linked

	second := s.WaitForCheck(s.RunCheck(p))
	if got := section(second, "vanished"); !sameSet(got, []string{"https://shop.test/b"}) {
		t.Errorf("second Check vanished = %q", got)
	}
	if text(second, ".vanished-count") != "1" || text(second, ".new-pages") != "0" {
		t.Errorf("counts: vanished %q new %q", text(second, ".vanished-count"), text(second, ".new-pages"))
	}
	third := s.WaitForCheck(s.RunCheck(p))
	if got := section(third, "vanished"); len(got) != 0 {
		t.Errorf("third Check vanished again: %q", got)
	}
	if got := section(third, "retired"); len(got) != 0 {
		t.Errorf("retired after only 2 orphaned Checks: %q", got)
	}
	fourth := s.WaitForCheck(s.RunCheck(p))
	if got := section(fourth, "retired"); !sameSet(got, []string{"https://shop.test/b"}) {
		t.Errorf("fourth Check retired = %q", got)
	}
	if text(fourth, ".retired-count") != "1" {
		t.Errorf("retired count %q", text(fourth, ".retired-count"))
	}

	hits := site.Hits("/b")
	fifth := s.WaitForCheck(s.RunCheck(p))
	if site.Hits("/b") != hits {
		t.Error("a retired Page was fetched")
	}
	if got := section(fifth, "retired"); len(got) != 0 {
		t.Errorf("retired listed again: %q", got)
	}
	if got := poolRows(t, s, p); !sameSet(got, []string{
		"https://shop.test/|active|200", "https://shop.test/a|active|200", "https://shop.test/b|retired|200"}) {
		t.Errorf("Page pool = %q", got)
	}
}

func TestARetiredPageLinkedAgainIsReactivatedAndListedAsNew(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/b">b</a>`))
	site.Set("/b", html(``))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", html(``))
	for i := 0; i < 3; i++ {
		s.WaitForCheck(s.RunCheck(p))
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE state = 'retired'`); n != 1 {
		t.Fatalf("%d retired Pages, want 1", n)
	}

	site.Set("/", html(`<a href="/b">b</a>`))
	report := s.WaitForCheck(s.RunCheck(p))

	if got := section(report, "new"); !sameSet(got, []string{"https://shop.test/b"}) {
		t.Errorf("new Pages = %q", got)
	}
	if text(report, ".new-pages") != "1" {
		t.Errorf("new count %q", text(report, ".new-pages"))
	}
	if text(report, ".fetched") != "2" {
		t.Errorf("fetched %q, want 2 (reactivated Page fetched in the same Check)", text(report, ".fetched"))
	}
	if got := poolRows(t, s, p); !sameSet(got, []string{"https://shop.test/|active|200", "https://shop.test/b|active|200"}) {
		t.Errorf("Page pool = %q", got)
	}
	if n := count(t, s, `SELECT count(*) FROM page_events WHERE kind = 'reactivated'`); n != 1 {
		t.Errorf("%d reactivated events", n)
	}
	// Linked again, the streak starts over.
	site.Set("/", html(``))
	again := s.WaitForCheck(s.RunCheck(p))
	if got := section(again, "vanished"); !sameSet(got, []string{"https://shop.test/b"}) {
		t.Errorf("vanished after reactivation = %q", got)
	}
}

func TestASelfLinkDoesNotKeepAPageFromBeingOrphaned(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(`<a href="/a">me</a><link rel="canonical" href="/a">`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", html(``))

	report := s.WaitForCheck(s.RunCheck(p))

	if got := section(report, "vanished"); !sameSet(got, []string{"https://shop.test/a"}) {
		t.Errorf("vanished = %q", got)
	}
}

func TestAFailingSeedSkipsOrphanCountingWithABanner(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(``))
	p := s.AddProject("shop.test")
	healthy := s.WaitForCheck(s.RunCheck(p))
	if healthy.Find("#orphan-banner").Length() != 0 {
		t.Error("banner shown on a healthy Check")
	}

	site.Fault("/", fakesite.Fault{Unreachable: true})
	for i := 0; i < 4; i++ {
		report := s.WaitForCheck(s.RunCheck(p))
		if report.Find("#orphan-banner").Length() != 1 {
			t.Fatalf("Check %d: no orphan-counting banner", i+1)
		}
		if len(section(report, "vanished"))+len(section(report, "retired")) != 0 {
			t.Fatalf("Check %d: orphans reported though the seed failed", i+1)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE state = 'retired' OR orphan_streak > 0`); n != 0 {
		t.Errorf("%d Pages had their orphan state changed", n)
	}
}

func TestFetchFailuresAndSeedAndManualPagesNeverRetire(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(``))
	site.Set("/m", html(``))
	p := s.AddProject("shop.test")
	s.Exec(`INSERT INTO pages (project_id, url, origin) VALUES ($1, 'https://shop.test/m', 'manual')`, p.ID)
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", html(``))
	site.Fault("/a", fakesite.Fault{Unreachable: true})
	for i := 0; i < 5; i++ {
		s.WaitForCheck(s.RunCheck(p))
	}
	rows := poolRows(t, s, p)
	for _, r := range rows {
		if strings.Contains(r, "|retired|") && !strings.HasPrefix(r, "https://shop.test/a|") {
			t.Errorf("seed or manual Page retired: %q", r)
		}
	}
	// /a is orphaned (no links) even though its Fetch fails: only its links decide.
	if n := count(t, s, `SELECT count(*) FROM pages WHERE origin <> 'discovered' AND (state <> 'active' OR orphan_streak > 0)`); n != 0 {
		t.Errorf("%d seed/manual Pages orphan-counted", n)
	}
}
