package systemtest_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// A running Check's progress poll and the Checks tab read the Check's running
// counters: neither counts Fetches or Pages, and the numbers equal what
// counting the rows gives.
func TestProgressOfARunningCheckComesFromItsCounters(t *testing.T) {
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

	wantQueries := func(got []string) {
		t.Helper()
		for _, name := range got {
			if !slices.Contains([]string{"CheckOwner", "GetCheck", "ListInFlightFetches", "GetCheckByNumber", "GetProject", "ListCheckRows", "CountChecks", "GetActiveCheck", "ProjectOwner"}, name) {
				t.Errorf("a progress screen ran %s (all: %v)", name, got)
			}
		}
	}
	var doc *goquery.Document
	queries := s.WebQueries(func() { doc = s.Page(s.GetHTMX(s.CheckPath(id) + "/progress")) })
	wantQueries(queries)

	rows := map[string]int{
		"known":      count(t, s, `SELECT count(*) FROM fetches WHERE check_id = $1`, id),
		"fetched":    count(t, s, `SELECT count(*) FROM fetches WHERE check_id = $1 AND state = 'done'`, id),
		"failures":   count(t, s, `SELECT count(*) FROM fetches WHERE check_id = $1 AND outcome = 'failure'`, id),
		"discovered": count(t, s, `SELECT count(*) FROM pages WHERE origin = 'discovered'`),
	}
	if rows["known"] != 3 || rows["fetched"] != 1 || rows["discovered"] != 2 {
		t.Fatalf("setup: rows %v", rows)
	}
	for cls, want := range rows {
		if got := atoiText(t, text(doc, "."+cls)); got != int64(want) {
			t.Errorf("progress .%s = %d, rows say %d", cls, got, want)
		}
	}

	var tab *goquery.Document
	queries = s.WebQueries(func() { tab = s.Page(s.Get("/p/" + itoa(p.ID))) })
	wantQueries(queries)
	if got := text(tab, "#checks td:nth-child(6)"); got != "1 / 3" {
		t.Errorf("Checks tab shows Fetched / Known = %q, want 1 / 3", got)
	}

	report := s.WaitForCheck(id)
	if text(report, ".fetched") != "3" {
		t.Errorf("report fetched %q, want 3", text(report, ".fetched"))
	}
	tab = s.Page(s.Get("/p/" + itoa(p.ID)))
	if got := text(tab, "#checks td:nth-child(6)"); got != "3 / 3" {
		t.Errorf("ended Check shows Fetched / Known = %q, want 3 / 3", got)
	}
}

func atoiText(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
