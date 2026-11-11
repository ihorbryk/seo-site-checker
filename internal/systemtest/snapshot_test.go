package systemtest_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// snapshotIDs returns the Snapshot ids of the seed Page, oldest first. No
// screen links to Snapshots yet, so the test reads them from the table.
func snapshotIDs(t *testing.T, s *systemtest.System) []int64 {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `SELECT id FROM snapshots ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// seoRows returns "field|v1;v2" for each row of the SEO fields tab.
func seoRows(d *goquery.Document) map[string]string {
	rows := map[string]string{}
	d.Find("#seo tr").Each(func(_ int, tr *goquery.Selection) {
		name := strings.TrimSpace(tr.Find("th").Text())
		if name == "" {
			return
		}
		var vals []string
		tr.Find("td li").Each(func(_ int, li *goquery.Selection) { vals = append(vals, strings.TrimSpace(li.Text())) })
		rows[name] = strings.Join(vals, ";")
	})
	return rows
}

func TestSnapshotPageShowsSeoFieldsHeadersAndRawHTML(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{
		Header: http.Header{"X-Robots-Tag": {"noarchive"}, "X-Custom": {"one"}},
		Body: `<html lang="en"><head><title>  Shop
		 Home </title><meta name="robots" content="noindex"><link rel="canonical" href="https://shop.test/">
		 </head><body><h1>Welcome</h1><h1>Again</h1></body></html>`,
	})
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	ids := snapshotIDs(t, s)
	if len(ids) != 1 {
		t.Fatalf("snapshots: %v", ids)
	}

	page := s.Page(s.Get("/s/" + itoa(ids[0])))

	head := text(page, "#snapshot-meta")
	for _, want := range []string{"200", "Check #1", "https://shop.test/"} {
		if !strings.Contains(head, want) {
			t.Errorf("header %q lacks %q", head, want)
		}
	}
	seo := seoRows(page)
	want := map[string]string{
		"title": "Shop Home", "meta_robots": "robots: noindex", "canonical": "https://shop.test/",
		"h1": "Welcome;Again", "html_lang": "en", "x_robots_tag": "noarchive", "status": "200",
		"og_title": "",
	}
	for k, v := range want {
		if got, ok := seo[k]; !ok || got != v {
			t.Errorf("seo %s = %q (present %v), want %q", k, got, ok, v)
		}
	}

	hdrs := s.Page(s.Get("/s/" + itoa(ids[0]) + "?tab=headers"))
	if got := text(hdrs, "#headers tr:contains(X-Custom) td"); got != "one" {
		t.Errorf("X-Custom header = %q", got)
	}

	raw := s.Page(s.Get("/s/" + itoa(ids[0]) + "?tab=html"))
	if got := raw.Find("pre.raw").Text(); !strings.Contains(got, `<h1>Welcome</h1>`) {
		t.Errorf("raw HTML = %q", got)
	}
}

func TestSnapshotPageForNonHTMLHasNoBody(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").Set("/", fakesite.Response{
		Header: http.Header{"Content-Type": {"application/pdf"}}, Body: "%PDF-1.4",
	})
	s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))
	ids := snapshotIDs(t, s)

	raw := s.Page(s.Get("/s/" + itoa(ids[0]) + "?tab=html"))
	if got := text(raw, "#no-body"); got != "No body stored" {
		t.Errorf("no-body note = %q", got)
	}
	seo := seoRows(s.Page(s.Get("/s/" + itoa(ids[0]))))
	if seo["title"] != "" || seo["content_type"] != "application/pdf" {
		t.Errorf("seo = %v", seo)
	}
}

func TestSnapshotPageLinksToPreviousAndNextSnapshotOfThePage(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{Body: "<html><title>One</title></html>"})
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", fakesite.Response{Body: "<html><title>Two</title></html>"})
	s.WaitForCheck(s.RunCheck(p))
	ids := snapshotIDs(t, s)
	if len(ids) != 2 {
		t.Fatalf("snapshots: %v", ids)
	}

	first := s.Page(s.Get("/s/" + itoa(ids[0])))
	second := s.Page(s.Get("/s/" + itoa(ids[1])))

	if href, _ := first.Find("a#next-snapshot").Attr("href"); href != "/s/"+itoa(ids[1]) {
		t.Errorf("next of first = %q", href)
	}
	if first.Find("a#prev-snapshot").Length() != 0 {
		t.Error("first Snapshot has a previous link")
	}
	if href, _ := second.Find("a#prev-snapshot").Attr("href"); href != "/s/"+itoa(ids[0]) {
		t.Errorf("prev of second = %q", href)
	}
	if second.Find("a#next-snapshot").Length() != 0 {
		t.Error("last Snapshot has a next link")
	}
	if got := seoRows(second)["title"]; got != "Two" {
		t.Errorf("second title = %q", got)
	}
}

func TestUnknownSnapshotIs404(t *testing.T) {
	s := systemtest.New(t)
	if w := s.Get("/s/9999"); w.Code != 404 {
		t.Errorf("status %d", w.Code)
	}
}
