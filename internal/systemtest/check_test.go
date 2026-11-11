package systemtest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

// text returns the trimmed text of the first element matching sel.
func text(d *goquery.Document, sel string) string {
	return strings.TrimSpace(d.Find(sel).First().Text())
}

// failureRows returns "url|kind" for each row of the Fetch failures section.
func failureRows(d *goquery.Document) []string {
	var rows []string
	d.Find("#failures tr").Each(func(i int, tr *goquery.Selection) {
		if i == 0 {
			return
		}
		rows = append(rows, strings.TrimSpace(tr.Find("td").Eq(0).Text())+"|"+strings.TrimSpace(tr.Find("td").Eq(1).Text()))
	})
	return rows
}

func count(t *testing.T, s *systemtest.System, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestA200HtmlSeedIsStoredAsASnapshotAndReported(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{Body: "<html><title>Shop</title></html>"})
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "1" || text(report, ".failed") != "0" {
		t.Errorf("counts: fetched %q failed %q", text(report, ".fetched"), text(report, ".failed"))
	}
	if text(report, "#report-header tr:contains(Trigger) td") != "manual" {
		t.Error("trigger not shown")
	}
	if len(failureRows(report)) != 0 {
		t.Error("expected no Fetch failures")
	}
	var status int
	var body string
	err := s.Pool.QueryRow(context.Background(), `
		SELECT s.status, convert_from(b.content, 'UTF8') FROM snapshots s
		JOIN bodies b ON b.sha256 = s.body_sha256`).Scan(&status, &body)
	if err != nil || status != 200 || !strings.Contains(body, "<title>Shop</title>") {
		t.Errorf("snapshot: status %d body %q err %v", status, body, err)
	}

	tab := s.Page(s.Get("/p/" + itoa(p.ID)))
	if got := text(tab, "#checks tr:nth-child(2) td:nth-child(3)"); got != "finished" {
		t.Errorf("Checks tab status = %q", got)
	}
}

func TestA500SeedIsASnapshotNotAFailure(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").Set("/", fakesite.Response{Status: 500, Body: "<html>oops</html>"})

	report := s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))

	if text(report, ".fetched") != "1" || text(report, ".failed") != "0" {
		t.Errorf("fetched %q failed %q", text(report, ".fetched"), text(report, ".failed"))
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots WHERE status = 500`); n != 1 {
		t.Errorf("%d 500-snapshots", n)
	}
}

func TestANonHtmlSeedGetsASnapshotWithoutABody(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").Set("/", fakesite.Response{
		Header: map[string][]string{"Content-Type": {"application/pdf"}}, Body: "%PDF-1.7",
	})

	report := s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))

	if text(report, ".fetched") != "1" {
		t.Errorf("fetched %q", text(report, ".fetched"))
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots WHERE status = 200 AND body_sha256 IS NULL`); n != 1 {
		t.Errorf("%d body-less snapshots", n)
	}
	if n := count(t, s, `SELECT count(*) FROM bodies`); n != 0 {
		t.Errorf("%d bodies stored for a non-HTML response", n)
	}
}

func TestATimeoutIsAFetchFailure(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{Body: "<html></html>"})
	site.Fault("/", fakesite.Fault{Delay: 5 * time.Second})
	p := s.AddProject("shop.test")
	s.Exec(`UPDATE projects SET timeout_s = 1 WHERE id = $1`, p.ID)

	report := s.WaitForCheck(s.RunCheck(p))

	if got := failureRows(report); len(got) != 1 || got[0] != "https://shop.test/|timeout" {
		t.Errorf("failures = %v", got)
	}
	if text(report, ".failed") != "1" || text(report, ".fetched") != "0" {
		t.Errorf("counts: failed %q fetched %q", text(report, ".failed"), text(report, ".fetched"))
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots`); n != 0 {
		t.Errorf("%d snapshots for a timeout", n)
	}
}

func TestADnsFailureIsAFetchFailure(t *testing.T) {
	s := systemtest.New(t) // no site for this host: the fake network fails DNS
	report := s.WaitForCheck(s.RunCheck(s.AddProject("gone.test")))

	if got := failureRows(report); len(got) != 1 || got[0] != "https://gone.test/|dns" {
		t.Errorf("failures = %v", got)
	}
}

func TestAnUnreachableSiteIsAConnectionFailure(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Fault("/", fakesite.Fault{Unreachable: true})

	report := s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))

	if got := failureRows(report); len(got) != 1 || got[0] != "https://shop.test/|connection" {
		t.Errorf("failures = %v", got)
	}
}

func TestAnOversizedHtmlBodyIsAFailureNotATruncatedSnapshot(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").OversizedBody("/", 10<<20+1)

	report := s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))

	if got := failureRows(report); len(got) != 1 || got[0] != "https://shop.test/|body_too_large" {
		t.Errorf("failures = %v", got)
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots`) + count(t, s, `SELECT count(*) FROM bodies`); n != 0 {
		t.Errorf("stored %d rows for an oversized body", n)
	}
}

func TestIdenticalBodiesAreStoredOncePerProjectAcrossChecks(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").Set("/", fakesite.Response{Body: "<html>same</html>"})
	p := s.AddProject("shop.test")

	s.WaitForCheck(s.RunCheck(p))
	s.WaitForCheck(s.RunCheck(p))

	if n := count(t, s, `SELECT count(*) FROM snapshots`); n != 2 {
		t.Errorf("%d snapshots, want 2", n)
	}
	if n := count(t, s, `SELECT count(*) FROM bodies`); n != 1 {
		t.Errorf("%d bodies, want 1", n)
	}
}

func TestPressingRunCheckWhileOneIsActiveShowsThatCheck(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{Body: "<html></html>"})
	site.Fault("/", fakesite.Fault{Delay: 500 * time.Millisecond})
	p := s.AddProject("shop.test")

	first := s.RunCheck(p)
	second := s.RunCheck(p)

	if first != second {
		t.Errorf("second press created Check %d, want the active Check %d", second, first)
	}
	if n := count(t, s, `SELECT count(*) FROM checks`); n != 1 {
		t.Errorf("%d Checks, want 1", n)
	}
	progress := s.Get("/c/" + itoa(first))
	if progress.Code != 200 || s.Page(progress).Find("#progress[hx-trigger='every 2s']").Length() != 1 {
		t.Errorf("active Check page is not the polling progress view (status %d)", progress.Code)
	}
	s.WaitForCheck(first)

	if third := s.RunCheck(p); third == first {
		t.Error("after the Check finished, Run Check should start a new one")
	}
}

func TestEveryActivePageIsFetchedOnceAcrossLimitedLanes(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	p := s.AddProject("shop.test")
	site.Set("/", fakesite.Response{Body: "<html>home</html>"})
	paths := []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g"}
	for _, path := range paths {
		testfixtures.Page(t, s.Pool, p, path)
		site.Set(path, fakesite.Response{Body: "<html>" + path + "</html>"})
	}
	s.Exec(`UPDATE projects SET max_parallel = 3 WHERE id = $1`, p.ID)

	report := s.WaitForCheck(s.RunCheck(p))

	if text(report, ".fetched") != "8" {
		t.Errorf("fetched %q, want 8", text(report, ".fetched"))
	}
	for _, path := range append(paths, "/") {
		if hits := site.Hits(path); hits != 1 {
			t.Errorf("%s fetched %d times", path, hits)
		}
	}
	var lanes int
	s.Pool.QueryRow(context.Background(), `SELECT lanes FROM checks`).Scan(&lanes)
	if lanes != 0 {
		t.Errorf("lanes = %d after finish", lanes)
	}
}

func TestReportSaysWhenAFailureAlsoFailedInThePreviousCheck(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{Body: "<html></html>"})
	site.Fault("/", fakesite.Fault{Unreachable: true})
	p := s.AddProject("shop.test")

	first := s.WaitForCheck(s.RunCheck(p))
	second := s.WaitForCheck(s.RunCheck(p))

	if first.Find(".also").Length() != 0 {
		t.Error("the first Check has no previous Check to also fail in")
	}
	if second.Find(".also").Length() != 1 {
		t.Error("the second Check should say the Page also failed in the previous Check")
	}

	site.Fault("/", fakesite.Fault{})
	third := s.WaitForCheck(s.RunCheck(p))
	if len(failureRows(third)) != 0 {
		t.Error("recovered Page still listed as failed")
	}
}

func TestProgressFragmentPollsUntilTheCheckEnds(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{Body: "<html></html>"})
	site.Fault("/", fakesite.Fault{Delay: 300 * time.Millisecond})
	p := s.AddProject("shop.test")
	id := s.RunCheck(p)

	w := s.Get("/c/" + itoa(id) + "/progress")
	d := s.Page(w)
	if w.Code != 200 || d.Find("#progress[hx-get][hx-trigger='every 2s']").Length() != 1 {
		t.Fatalf("progress fragment: status %d %s", w.Code, w.Body.String())
	}

	s.WaitForCheck(id)
	w = s.Get("/c/" + itoa(id) + "/progress")
	if w.Header().Get("HX-Redirect") != "/c/"+itoa(id) {
		t.Errorf("finished Check should redirect the fragment to its Report, got %q", w.Header().Get("HX-Redirect"))
	}
}

func TestUnknownProjectsAndChecksAre404(t *testing.T) {
	s := systemtest.New(t)
	for _, path := range []string{"/p/999", "/c/999", "/c/999/progress", "/p/abc", "/c/0"} {
		if w := s.Get(path); w.Code != 404 {
			t.Errorf("GET %s: status %d", path, w.Code)
		}
	}
	if w := s.Post("/p/999/checks", nil); w.Code != 404 {
		t.Errorf("POST run check on a missing Project: status %d", w.Code)
	}
}
