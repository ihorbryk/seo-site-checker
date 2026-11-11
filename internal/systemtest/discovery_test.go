package systemtest_test

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func html(body string) fakesite.Response {
	return fakesite.Response{Body: "<html><body>" + body + "</body></html>"}
}

// poolRows returns "url|state|status" for each row of the Page pool tab.
func poolRows(t *testing.T, s *systemtest.System, p dbgen.Project) []string {
	t.Helper()
	w := s.Get("/p/" + itoa(p.ID) + "/pages")
	if w.Code != 200 {
		t.Fatalf("Page pool tab: status %d", w.Code)
	}
	var rows []string
	s.Page(w).Find("#pages tr").Each(func(i int, tr *goquery.Selection) {
		if i == 0 {
			return
		}
		td := tr.Find("td")
		rows = append(rows, strings.TrimSpace(td.Eq(0).Text())+"|"+strings.TrimSpace(td.Eq(1).Text())+"|"+strings.TrimSpace(td.Eq(2).Text()))
	})
	sort.Strings(rows)
	return rows
}

func sameSet(got, want []string) bool {
	sort.Strings(got)
	sort.Strings(want)
	return strings.Join(got, "\n") == strings.Join(want, "\n")
}

func TestALinkedSiteIsFullyFetchedInOneCheck(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b/">b</a><a href="#top">self</a>`))
	site.Set("/a", html(`<a href="/a/deep">deep</a><a href="/">home</a><link rel="canonical" href="/a">`))
	site.Set("/b/", html(`<link rel="alternate" hreflang="de" href="/de">`))
	site.Set("/a/deep", html(`<a href="/a?utm_source=x">back</a>`))
	site.Set("/de", html(`end`))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "5" {
		t.Errorf("fetched %q, want 5", text(report, ".fetched"))
	}
	if text(report, ".new-pages") != "4" {
		t.Errorf("new Pages %q, want 4", text(report, ".new-pages"))
	}
	if text(report, ".dropped") != "0" {
		t.Errorf("dropped %q, want 0", text(report, ".dropped"))
	}
	for _, path := range []string{"/", "/a", "/b/", "/a/deep", "/de"} {
		if hits := site.Hits(path); hits != 1 {
			t.Errorf("%s fetched %d times", path, hits)
		}
	}
	want := []string{
		"https://shop.test/|active|200", "https://shop.test/a|active|200", "https://shop.test/b/|active|200",
		"https://shop.test/a/deep|active|200", "https://shop.test/de|active|200",
	}
	if got := poolRows(t, s, p); !sameSet(got, want) {
		t.Errorf("Page pool = %q, want %q", got, want)
	}
	// Self-links are stored too: the seed links to itself via #top, /a via canonical.
	if n := count(t, s, `SELECT count(*) FROM links l JOIN snapshots s ON s.id = l.snapshot_id WHERE s.page_id = l.target_page_id`); n != 2 {
		t.Errorf("%d self-links stored, want 2 (seed via #top, /a via canonical)", n)
	}
}

func TestOffHostAndSubdomainLinksAreIgnored(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="https://www.shop.test/x">sub</a><a href="https://other.test/y">other</a>
		<a href="mailto:a@shop.test">m</a><a href="/in">in</a>`))
	site.Set("/in", html(``))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "2" || text(report, ".new-pages") != "1" {
		t.Errorf("fetched %q new %q", text(report, ".fetched"), text(report, ".new-pages"))
	}
	if n := count(t, s, `SELECT count(*) FROM pages`); n != 2 {
		t.Errorf("%d Pages in the pool, want 2", n)
	}
}

func TestSlashVariantsAreSeparatePages(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">1</a><a href="/a/">2</a>`))
	site.Set("/a", html(`one`))
	site.Set("/a/", html(`two`))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "3" {
		t.Errorf("fetched %q, want 3", text(report, ".fetched"))
	}
	if site.Hits("/a") != 1 || site.Hits("/a/") != 1 {
		t.Errorf("/a fetched %d, /a/ fetched %d times", site.Hits("/a"), site.Hits("/a/"))
	}
}

func TestACrawlerTrapIsDroppedAndCounted(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	long := "/" + strings.Repeat("a", 2100)
	site.Set("/", html(`<a href="/x/y/x/z/x">loop</a><a href="`+long+`">long</a><a href="/ok">ok</a>`))
	site.Set("/ok", html(``))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "2" || text(report, ".dropped") != "2" || text(report, ".new-pages") != "1" {
		t.Errorf("fetched %q dropped %q new %q", text(report, ".fetched"), text(report, ".dropped"), text(report, ".new-pages"))
	}
	if site.Hits("/x/y/x/z/x") != 0 {
		t.Error("a dropped URL was fetched")
	}
	if n := count(t, s, `SELECT count(*) FROM pages`); n != 2 {
		t.Errorf("%d Pages, want 2", n)
	}
}

func TestLaneCountRisesToMaxParallelAndNeverExceedsIt(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	var links strings.Builder
	for i := 0; i < 12; i++ {
		path := "/p" + strconv.Itoa(i)
		links.WriteString(`<a href="` + path + `">x</a>`)
		site.Set(path, html(``))
		site.Fault(path, fakesite.Fault{Delay: 150 * time.Millisecond})
	}
	site.Set("/", html(links.String()))
	p := s.AddProject("shop.test")
	s.Exec(`UPDATE projects SET max_parallel = 3 WHERE id = $1`, p.ID)

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "13" {
		t.Errorf("fetched %q, want 13", text(report, ".fetched"))
	}
	if got := site.MaxConcurrent(); got != 3 {
		t.Errorf("peak concurrent requests = %d, want exactly max_parallel (3)", got)
	}
	if n := count(t, s, `SELECT lanes FROM checks`); n != 0 {
		t.Errorf("lanes = %d after finish", n)
	}
}

func TestPagePoolTabListsPagesWithStateAndLastStatus(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/gone">g</a>`))
	p := s.AddProject("shop.test")

	if got := poolRows(t, s, p); !sameSet(got, []string{"https://shop.test/|active|—"}) {
		t.Errorf("before any Check: %q", got)
	}
	s.WaitForCheck(s.RunCheck(p))

	want := []string{"https://shop.test/|active|200", "https://shop.test/gone|active|404"}
	if got := poolRows(t, s, p); !sameSet(got, want) {
		t.Errorf("Page pool = %q, want %q", got, want)
	}
	if w := s.Get("/p/999/pages"); w.Code != 404 {
		t.Errorf("unknown Project: status %d", w.Code)
	}
}

func TestNewPagesAreCountedPerCheckNotRepeated(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(``))
	p := s.AddProject("shop.test")

	first := s.WaitForCheck(s.RunCheck(p))
	second := s.WaitForCheck(s.RunCheck(p))

	if text(first, ".new-pages") != "1" || text(second, ".new-pages") != "0" {
		t.Errorf("new Pages: first %q, second %q", text(first, ".new-pages"), text(second, ".new-pages"))
	}
	if text(second, ".fetched") != "2" {
		t.Errorf("second Check fetched %q, want 2", text(second, ".fetched"))
	}
}
