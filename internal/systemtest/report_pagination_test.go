package systemtest_test

import (
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// Like the other pagination tests, these Reports are built from rows inserted
// up front with no worker running, and their order is the behaviour under
// test, so rows are compared in order.

// largeReport is an ended Check of shop.test whose Report has 120 changed
// Pages c001…c120 (c010, c020, … critical; other even ones SEO; odd ones
// body; every third one redirects), 30 unchanged u001…u030, changed Pages
// that are excluded (/private/…) or ignored, 120 new Pages (the c ones, plus
// the ignored ones, left out), 30 vanished (the u ones), 75 fetch failures
// f001…f075 (also retired) and 60 skipped Pages s001…s060. It returns the Report's path.
func largeReport(t *testing.T, s *systemtest.System) string {
	t.Helper()
	p := s.AddProject("shop.test")
	s.SaveRequestSettings(p, settingsForm("/private/**\n"))
	checkID := count(t, s, `INSERT INTO checks (project_id, trigger, state, started_at, finished_at)
		VALUES ($1, 'manual', 'finished', now(), now()) RETURNING id`, p.ID)
	pages := func(prefix string, n int, state string) {
		s.Exec(`INSERT INTO pages (project_id, url, origin, state)
			SELECT $1, 'https://shop.test/' || $2::text || lpad(n::text, 3, '0'), 'discovered', $4
			FROM generate_series(1, $3::int) AS n`, p.ID, prefix, n, state)
	}
	fetches := func(prefix, outcome, extra string) {
		s.Exec(`INSERT INTO fetches (check_id, page_id, state, outcome, failure_kind, skip_reason)
			SELECT $1, id, 'done', $3::text,
				CASE WHEN $3::text = 'failure' THEN $4 END, CASE WHEN $3::text = 'skipped' THEN $4 END
			FROM pages WHERE project_id = $2 AND url LIKE 'https://shop.test/' || $5::text || '%'`,
			checkID, p.ID, outcome, extra, prefix)
	}
	pages("c", 120, "active")
	pages("u", 30, "active")
	pages("private/x", 3, "active")
	pages("ign", 2, "ignored")
	pages("f", 75, "active")
	pages("s", 60, "active")
	for _, prefix := range []string{"c", "u", "private/x", "ign"} {
		fetches(prefix, "snapshot", "")
	}
	fetches("f", "failure", "timeout")
	fetches("s", "skipped", "robots_unreachable")
	s.Exec(`INSERT INTO snapshots (id, page_id, check_id, fetched_at, status, headers, seo_fields)
		SELECT f.id, f.page_id, f.check_id, now(),
			CASE WHEN redirects THEN 301 ELSE 200 END, '[]',
			CASE WHEN redirects THEN jsonb_build_object('redirect_target', jsonb_build_array(p.url || '-to')) ELSE '{}' END
		FROM fetches f JOIN pages p ON p.id = f.page_id,
			LATERAL (SELECT p.url ~ '/c[0-9]+$' AND right(p.url, 3)::int % 3 = 0 AS redirects) r
		WHERE f.check_id = $1 AND f.outcome = 'snapshot'`, checkID)
	s.Exec(`INSERT INTO diffs (snapshot_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, tier)
		SELECT sn.id, sn.id, false, false, false, tier <> 'empty', tier
		FROM snapshots sn JOIN pages p ON p.id = sn.page_id,
			LATERAL (SELECT CASE
				WHEN p.url ~ '/u[0-9]+$' THEN 'empty'
				WHEN p.url ~ '/c[0-9]+$' AND right(p.url, 3)::int % 10 = 0 THEN 'critical'
				WHEN p.url ~ '/c[0-9]+$' AND right(p.url, 3)::int % 2 = 0 THEN 'seo'
				WHEN p.url ~ '/c[0-9]+$' THEN 'body'
				ELSE 'critical' END AS tier) t
		WHERE sn.check_id = $1`, checkID)
	s.Exec(`INSERT INTO page_events (check_id, page_id, kind)
		SELECT $1, id, CASE WHEN url ~ '/[ci][a-z]*[0-9]+$' THEN 'new' WHEN url ~ '/u[0-9]+$' THEN 'vanished' ELSE 'retired' END
		FROM pages WHERE project_id = $2 AND url ~ '/([cuf]|ign)[0-9]+$'`, checkID, p.ID)
	s.Exec(`UPDATE checks SET count_new = 120, count_vanished = 30, count_retired = 75, count_failures = 75, count_skipped = 60 WHERE id = $1`, checkID)
	return "/c/" + itoa(int64(checkID))
}

// numbered is "https://shop.test/<prefix>NNN" for each n.
func numbered(prefix string, ns ...int) []string {
	var urls []string
	for _, n := range ns {
		urls = append(urls, fmt.Sprintf("https://shop.test/%s%03d", prefix, n))
	}
	return urls
}

func upTo(from, to int) []int {
	var ns []int
	for n := from; n <= to; n++ {
		ns = append(ns, n)
	}
	return ns
}

// rankedChanged is the Changed Pages of largeReport in Report order: tier,
// then URL.
func rankedChanged() []string {
	var critical, seo, body []int
	for n := 1; n <= 120; n++ {
		switch {
		case n%10 == 0:
			critical = append(critical, n)
		case n%2 == 0:
			seo = append(seo, n)
		default:
			body = append(body, n)
		}
	}
	return numbered("c", slices.Concat(critical, seo, body)...)
}

// cells is the text of the first cell of each data row of table.
func cells(d *goquery.Document, table string) []string {
	var got []string
	d.Find(table + " tr").Each(func(i int, tr *goquery.Selection) {
		if i > 0 {
			got = append(got, strings.TrimSpace(tr.Find("td").First().Text()))
		}
	})
	return got
}

func sectionPager(d *goquery.Document, id string) *goquery.Selection {
	return d.Find("#" + id + "-section .pager")
}

func TestALargeReportShowsOnePageOfChangedPagesRankedByTierThenURL(t *testing.T) {
	s := systemtest.New(t)
	path := largeReport(t, s)
	want := rankedChanged()

	d := s.Page(s.Get(path))
	if got := cells(d, "#changed"); !slices.Equal(got, want[:50]) {
		t.Errorf("first page of Changed Pages = %v, want %v", got, want[:50])
	}
	if txt := strings.Join(strings.Fields(sectionPager(d, "changed").Text()), " "); !strings.Contains(txt, "Page 1 of 3") || !strings.Contains(txt, "Rows 1–50 of 120") {
		t.Errorf("Changed pager = %q", txt)
	}
	if got := cells(s.Page(s.Get(path+"?changed_page=2")), "#changed"); !slices.Equal(got, want[50:100]) {
		t.Errorf("changed_page=2 = %v, want rows 51–100 %v", got, want[50:100])
	}
	// Walk Changed Pages by its own Next link (other sections have pagers too).
	var got []string
	for next, pages := path, 0; next != ""; pages++ {
		if pages == 3 {
			t.Fatal("Changed Pages has more than 3 pages")
		}
		d := s.Page(s.Get(strings.SplitN(next, "#", 2)[0]))
		got = append(got, cells(d, "#changed")...)
		next = sectionPager(d, "changed").Find("a.next").AttrOr("href", "")
	}
	if !slices.Equal(got, want) {
		t.Errorf("Changed Pages across pages = %v, want %v", got, want)
	}
}

func TestReportCountsCoverTheWholeCheckOnEveryPage(t *testing.T) {
	s := systemtest.New(t)
	path := largeReport(t, s)
	for _, q := range []string{"", "?changed_page=3", "?changed_per=500", "?changed_per=100&changed_page=2", "?new_page=2&failures_per=200", "?show=skipped&skipped_page=2"} {
		d := s.Page(s.Get(path + q))
		// The excluded and ignored Pages are left out of Changed.
		header := []string{text(d, ".changed"), text(d, ".unchanged"), text(d, ".new-pages"), text(d, ".vanished-count"), text(d, ".failed"), text(d, ".skipped")}
		if want := []string{"120", "30", "120", "30", "75", "60"}; !slices.Equal(header, want) {
			t.Errorf("%s: header counts %v, want %v", q, header, want)
		}
		// The ignored Pages' new events are left out of New Pages too.
		for id, total := range map[string]string{"changed": "of 120", "new": "of 120", "retired": "of 75", "failures": "of 75"} {
			if txt := sectionPager(d, id).Find(".rows").Text(); !strings.HasSuffix(txt, total) {
				t.Errorf("%s: %s pager rows %q, want %q", q, id, txt, total)
			}
		}
	}
}

// The per-row cost has no screen of its own, so these tests count the
// queries the web app ran (System.WebQueries).

func TestOnlyChangedPagesOnTheCurrentPageGetTheirRedirectChainsBuilt(t *testing.T) {
	s := systemtest.New(t)
	path := largeReport(t, s)
	var d *goquery.Document
	queries := s.WebQueries(func() { d = s.Page(s.Get(path + "?changed_page=2")) })

	chains := d.Find("#changed .chain").Length()
	lookups := 0
	for _, q := range queries {
		if q == "CheckSnapshotRedirect" {
			lookups++
		}
	}
	// 40 of the 120 changed Pages redirect; each chain here is one hop to a
	// URL with no Snapshot: two lookups.
	if chains == 0 || chains >= 40 || lookups != 2*chains {
		t.Errorf("%d chains on the page, %d chain lookups; want lookups only for the page's %d redirecting rows", chains, lookups, chains)
	}
	if got := text(d, "#changed .chain"); !strings.HasSuffix(got, "-to") {
		t.Errorf("chain = %q", got)
	}
}

func TestEachReportSectionPaginatesIndependently(t *testing.T) {
	s := systemtest.New(t)
	path := largeReport(t, s)
	changed := rankedChanged()

	d := s.Page(s.Get(path + "?show=skipped&new_page=2&failures_page=2&skipped_per=100&retired_page=2&retired_per=50"))
	for _, tc := range []struct {
		table string
		want  []string
	}{
		{"#changed", changed[:50]},
		{"#new", numbered("c", upTo(51, 100)...)},
		{"#vanished", numbered("u", upTo(1, 30)...)},
		{"#retired", numbered("f", upTo(51, 75)...)},
		{"#failures", numbered("f", upTo(51, 75)...)},
		{"#skipped-list", numbered("s", upTo(1, 60)...)},
	} {
		if got := cells(d, tc.table); !slices.Equal(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.table, got, tc.want)
		}
	}

	// A section with 50 rows or fewer has no pager; a longer one keeps its
	// pager even when it fits on one page.
	for id, n := range map[string]int{"changed": 1, "new": 1, "vanished": 0, "retired": 1, "failures": 1, "skipped": 1} {
		if got := sectionPager(d, id).Length(); got != n {
			t.Errorf("%s section has %d pagers, want %d", id, got, n)
		}
	}
	if sectionPager(d, "skipped").Find(".pages").Length() != 0 {
		t.Error("skipped fits on one page at 100 rows but shows Prev/Next")
	}

	// Moving one section keeps the others' parameters, lands on its anchor and
	// keeps show=skipped; changing per goes back to page 1.
	next := sectionPager(d, "new").Find("a.next").AttrOr("href", "")
	for _, part := range []string{"new_page=3", "failures_page=2", "skipped_per=100", "show=skipped"} {
		if !strings.Contains(next, part) {
			t.Errorf("New Pages Next = %q, missing %s", next, part)
		}
	}
	if !strings.HasPrefix(next, path+"?") || !strings.HasSuffix(next, "#new") {
		t.Errorf("New Pages Next = %q, want the Report URL ending in #new", next)
	}
	var per200 string
	sectionPager(d, "failures").Find(".per a").Each(func(_ int, a *goquery.Selection) {
		if a.Text() == "200" {
			per200 = a.AttrOr("href", "")
		}
	})
	if !strings.Contains(per200, "failures_per=200") || strings.Contains(per200, "failures_page") || !strings.Contains(per200, "new_page=2") {
		t.Errorf("failures 200 rows link = %q, want failures page 1 keeping new_page", per200)
	}
	if href := sectionPager(d, "skipped").Find(".per a").First().AttrOr("href", ""); !strings.Contains(href, "show=skipped") {
		t.Errorf("skipped pager link %q drops show=skipped", href)
	}
	if s.Page(s.Get(path)).Find("#skipped-list").Length() != 0 {
		t.Error("skipped list shown without show=skipped")
	}
}

func TestBadReportSectionParametersAreHandledLeniently(t *testing.T) {
	s := systemtest.New(t)
	path := largeReport(t, s)
	changed := rankedChanged()
	for _, q := range []string{"?changed_page=0", "?changed_page=-3", "?changed_page=abc", "?changed_per=75", "?changed_per=abc", "?page=2&per=100"} {
		w := s.Get(path + q)
		if got := cells(s.Page(w), "#changed"); w.Code != 200 || !slices.Equal(got, changed[:50]) {
			t.Errorf("%s: status %d, %d rows, want page 1 of 50 rows", q, w.Code, len(got))
		}
	}
	if got := cells(s.Page(s.Get(path+"?failures_page=99")), "#failures"); !slices.Equal(got, numbered("f", upTo(51, 75)...)) {
		t.Errorf("failures_page=99: %v, want the last page", got)
	}
}

func TestAReportSectionPagerSwapsOnlyThatSectionThroughHTMX(t *testing.T) {
	s := systemtest.New(t)
	path := largeReport(t, s)
	start := path + "?new_page=2&changed_per=100"
	d := s.Page(s.Get(start))

	next := sectionPager(d, "failures").Find("a.next")
	hx, push, href := next.AttrOr("hx-get", ""), next.AttrOr("hx-push-url", ""), next.AttrOr("href", "")
	if hx == "" || push != href || !strings.Contains(push, "failures_page=2") || !strings.Contains(push, "new_page=2") {
		t.Fatalf("failures Next: hx-get %q, hx-push-url %q, href %q", hx, push, href)
	}
	if target := sectionPager(d, "failures").AttrOr("hx-target", ""); target != "#failures-section" {
		t.Errorf("failures pager hx-target = %q", target)
	}

	var w *httptest.ResponseRecorder
	queries := s.WebQueries(func() { w = s.GetHTMX(hx) })
	frag := s.Page(w)
	if w.Code != 200 || frag.Find("#failures-section").Length() != 1 || frag.Find("#report-header, #changed, #new").Length() != 0 {
		t.Errorf("section fragment: status %d, body %.200s", w.Code, w.Body.String())
	}
	slices.Sort(queries)
	if want := []string{"CountFetchFailures", "GetCheck", "GetProject", "ListFetchFailures"}; !slices.Equal(queries, want) {
		t.Errorf("section request ran %v, want only %v", queries, want)
	}

	// The pushed URL, loaded in full, shows the fragment's rows and leaves
	// every other section where it was.
	// A browser never sends the #fragment.
	full := s.Page(s.Get(strings.SplitN(push, "#", 2)[0]))
	before := s.Page(s.Get(start))
	if got, want := cells(full, "#failures"), cells(frag, "#failures"); !slices.Equal(got, want) || len(got) != 25 {
		t.Errorf("pushed URL failures = %v, fragment %v", got, want)
	}
	for _, table := range []string{"#changed", "#new", "#vanished"} {
		if got, want := cells(full, table), cells(before, table); !slices.Equal(got, want) {
			t.Errorf("%s changed after paging failures: %v, was %v", table, got, want)
		}
	}

	// The costly Changed section alone classifies the Check, loads one page
	// and builds only that page's chains.
	changedHx := sectionPager(d, "changed").Find("a.next").AttrOr("hx-get", "")
	queries = s.WebQueries(func() { w = s.GetHTMX(changedHx) })
	ran := map[string]bool{}
	for _, q := range queries {
		ran[q] = true
	}
	for q := range ran {
		if !slices.Contains([]string{"GetCheck", "GetProject", "ListCheckDiffStates", "ListCheckDiffRows", "CheckSnapshotRedirect"}, q) {
			t.Errorf("Changed section request ran %s (all: %v)", q, queries)
		}
	}
	if got := cells(s.Page(w), "#changed"); len(got) != 20 || !slices.Equal(got, rankedChanged()[100:]) {
		t.Errorf("Changed fragment page 2 at 100 per page = %v", got)
	}

	// Without htmx the section URL sends the browser to the Report.
	if w := s.Get(hx); w.Code != 303 || w.Header().Get("Location") != path+"?"+strings.SplitN(hx, "?", 2)[1]+"#failures" {
		t.Errorf("full load of section URL: status %d, Location %q", w.Code, w.Header().Get("Location"))
	}
}
