package systemtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

const pageA = `<html><head><title>Shop</title><meta name="description" content="d"></head><body><h1>Hi</h1><p>hello</p></body></html>`

// changedRows returns "url|tier" for each row of the Changed Pages section.
func changedRows(d *goquery.Document) []string {
	var rows []string
	d.Find("#changed tr").Each(func(i int, tr *goquery.Selection) {
		if i == 0 {
			return
		}
		rows = append(rows, strings.TrimSpace(tr.Find("td").Eq(0).Text())+"|"+strings.TrimSpace(tr.Find(".tier").Text()))
	})
	return rows
}

// twoChecks runs a Check on a fresh site serving the home page, calls change
// to alter the site, runs a second Check and returns its Report.
func twoChecks(t *testing.T, home fakesite.Response, change func(site *fakesite.Site)) (*systemtest.System, *goquery.Document, *goquery.Document) {
	t.Helper()
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", home)
	p := s.AddProject("shop.test")
	first := s.WaitForCheck(s.RunCheck(p))
	change(site)
	second := s.WaitForCheck(s.RunCheck(p))
	return s, first, second
}

func html(body string, h ...string) fakesite.Response {
	r := fakesite.Response{Body: body}
	if len(h) > 0 {
		r.Header = map[string][]string{}
		for i := 0; i+1 < len(h); i += 2 {
			r.Header[h[i]] = append(r.Header[h[i]], h[i+1])
		}
	}
	return r
}

func TestAFirstSnapshotHasNoDiffAndTheReportCountsNothingChanged(t *testing.T) {
	s, first, _ := twoChecks(t, html(pageA), func(*fakesite.Site) {})
	if got := changedRows(first); len(got) != 0 {
		t.Errorf("first Check lists changed Pages: %v", got)
	}
	if n := count(t, s, `SELECT count(*) FROM diffs`); n != 1 {
		t.Errorf("%d Diff rows after two Checks, want 1 (none for the first Snapshot)", n)
	}
}

func TestATitleChangeRanksSEOWithBeforeAfter(t *testing.T) {
	_, first, second := twoChecks(t, html(pageA), func(site *fakesite.Site) {
		site.Set("/", html(strings.Replace(pageA, "<title>Shop</title>", "<title>Store</title>", 1)))
	})
	if got := changedRows(first); len(got) != 0 {
		t.Errorf("first Check: %v", got)
	}
	if got := changedRows(second); len(got) != 1 || got[0] != "https://shop.test/|seo" {
		t.Fatalf("changed = %v", got)
	}
	if got := text(second, "#changed .seo"); !strings.Contains(got, "title") || !strings.Contains(got, "Shop → Store") {
		t.Errorf("SEO before → after = %q", got)
	}
	if text(second, ".changed") != "1" || text(second, ".unchanged") != "0" {
		t.Errorf("counts: changed %q unchanged %q", text(second, ".changed"), text(second, ".unchanged"))
	}
}

func TestANoindexRanksCritical(t *testing.T) {
	_, _, second := twoChecks(t, html(pageA), func(site *fakesite.Site) {
		site.Set("/", html(strings.Replace(pageA, "<head>", `<head><meta name="robots" content="noindex">`, 1)))
	})
	if got := changedRows(second); len(got) != 1 || got[0] != "https://shop.test/|critical" {
		t.Errorf("changed = %v", got)
	}
	if got := text(second, "#changed .seo"); !strings.Contains(got, "robots: noindex") {
		t.Errorf("SEO = %q", got)
	}
}

func TestAHeaderOnlyChangeRanksHeaders(t *testing.T) {
	_, _, second := twoChecks(t, html(pageA, "X-Powered-By", "a"), func(site *fakesite.Site) {
		site.Set("/", html(pageA, "X-Powered-By", "b"))
	})
	if got := changedRows(second); len(got) != 1 || got[0] != "https://shop.test/|headers" {
		t.Errorf("changed = %v", got)
	}
}

func TestABodyOnlyChangeRanksBody(t *testing.T) {
	_, _, second := twoChecks(t, html(pageA), func(site *fakesite.Site) {
		site.Set("/", html(strings.Replace(pageA, "hello", "goodbye", 1)))
	})
	if got := changedRows(second); len(got) != 1 || got[0] != "https://shop.test/|body" {
		t.Errorf("changed = %v", got)
	}
}

func TestADateAndNonceOnlyChangeIsAnEmptyDiff(t *testing.T) {
	withNonce := func(n string) string {
		return strings.Replace(pageA, "<body>", `<body><script nonce="`+n+`">var a=1</script>`, 1)
	}
	s, _, second := twoChecks(t, html(withNonce("aaa"), "Date", "Mon, 01 Jan 2024 00:00:00 GMT"), func(site *fakesite.Site) {
		site.Set("/", html(withNonce("bbb"), "Date", "Tue, 02 Jan 2024 00:00:00 GMT"))
	})
	if got := changedRows(second); len(got) != 0 {
		t.Errorf("changed = %v", got)
	}
	if text(second, ".changed") != "0" || text(second, ".unchanged") != "1" {
		t.Errorf("counts: changed %q unchanged %q", text(second, ".changed"), text(second, ".unchanged"))
	}
	var tier string
	var baseline, snap int64
	err := s.Pool.QueryRow(context.Background(), `SELECT tier, baseline_id, snapshot_id FROM diffs`).Scan(&tier, &baseline, &snap)
	if err != nil || tier != "empty" || baseline >= snap {
		t.Errorf("Diff row: tier %q baseline %d snapshot %d err %v", tier, baseline, snap, err)
	}
}

func TestChangedPagesAreSortedByTierThenURL(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	p := s.AddProject("shop.test")
	pages := map[string]string{"/": pageA, "/a": pageA, "/b": pageA, "/c": pageA, "/d": pageA}
	for path, body := range pages {
		if path != "/" {
			testfixtures.Page(t, s.Pool, p, path)
		}
		site.Set(path, html(body))
	}
	s.WaitForCheck(s.RunCheck(p))

	site.Set("/", html(strings.Replace(pageA, "hello", "bye", 1)))                                            // body
	site.Set("/a", html(strings.Replace(pageA, "<title>Shop", "<title>Other", 1)))                            // seo
	site.Set("/b", html(strings.Replace(pageA, "<head>", `<head><meta name="robots" content="noindex">`, 1))) // critical
	site.Set("/c", html(strings.Replace(pageA, "<title>Shop", "<title>Other", 1)))                            // seo
	// /d unchanged
	report := s.WaitForCheck(s.RunCheck(p))

	want := []string{
		"https://shop.test/b|critical", "https://shop.test/a|seo", "https://shop.test/c|seo", "https://shop.test/|body",
	}
	got := changedRows(report)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("changed = %v, want %v", got, want)
	}
	if text(report, ".changed") != "4" || text(report, ".unchanged") != "1" {
		t.Errorf("counts: changed %q unchanged %q", text(report, ".changed"), text(report, ".unchanged"))
	}
}
