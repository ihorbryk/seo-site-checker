package systemtest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// firstCells returns the text of the first link in each data row of table.
func firstCells(d *goquery.Document, table string) []string {
	var got []string
	d.Find(table + " tr").Each(func(i int, tr *goquery.Selection) {
		if i > 0 {
			got = append(got, strings.TrimSpace(tr.Find("td").First().Find("a").First().Text()))
		}
	})
	return got
}

// checkIDsNewestFirst is "#n" … "#1": n Checks inserted in id order.
func checkIDsNewestFirst(n int) []string {
	var ids []string
	for i := n; i >= 1; i-- {
		ids = append(ids, fmt.Sprintf("#%d", i))
	}
	return ids
}

// Unlike Check scenarios, these lists are rendered from rows inserted up
// front with no worker running, and their order is the behaviour under test,
// so they are compared in order.

func pagerText(d *goquery.Document) string {
	return strings.Join(strings.Fields(d.Find(".pager").First().Text()), " ")
}

func TestTheChecksTabShowsOnePageOfChecksAtATime(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	// 120 ended Checks created at the same instant: ties fall back to id.
	s.Exec(`INSERT INTO checks (project_id, trigger, state, created_at, started_at, finished_at)
		SELECT $1, 'manual', 'finished', '2026-01-01Z', '2026-01-01Z', '2026-01-01Z' FROM generate_series(1, 120)`, p.ID)
	want := checkIDsNewestFirst(120)
	base := "/p/" + itoa(p.ID)

	got, pages := s.PagedRows(base, "#checks", 50)
	if pages != 3 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%d pages of Checks %v, want 3 pages of %v", pages, got, want)
	}
	if got, _ := s.PagedRows(base+"?per=100", "#checks", 100); len(got) != 120 {
		t.Errorf("per=100: %d Checks across pages", len(got))
	}

	first := s.Page(s.Get(base))
	if txt := pagerText(first); !strings.Contains(txt, "Page 1 of 3") || !strings.Contains(txt, "Rows 1–50 of 120") {
		t.Errorf("pager = %q", txt)
	}
	if first.Find(".pager a.prev").Length() != 0 || first.Find(".pager").Length() != 2 {
		t.Errorf("first page: Prev is a link or the pager is not above and below the table")
	}
	for _, q := range []string{"?page=0", "?page=-3", "?page=abc", "?per=75", "?per=abc"} {
		w := s.Get(base + q)
		if d := s.Page(w); w.Code != 200 || len(firstCells(d, "#checks")) != 50 || firstCells(d, "#checks")[0] != "#120" {
			t.Errorf("%s: status %d, %d rows, want page 1 of 50 rows", q, w.Code, len(firstCells(d, "#checks")))
		}
	}
	last := s.Page(s.Get(base + "?page=99&per=100"))
	if got := firstCells(last, "#checks"); len(got) != 20 || got[0] != "#20" {
		t.Errorf("page past the end: %v, want the last page", got)
	}
	if last.Find(".pager a.next").Length() != 0 || last.Find(".pager a.prev").Length() == 0 {
		t.Error("last page: Next is a link or Prev is not")
	}
}

func TestPageHistoryShowsOnePageOfChecksAtATime(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	seed := s.PageID(p, "https://shop.test/")
	s.Exec(`INSERT INTO checks (project_id, trigger, state, started_at, finished_at)
		SELECT $1, 'manual', 'cancelled', now(), now() FROM generate_series(1, 120)`, p.ID)
	s.Exec(`INSERT INTO fetches (check_id, page_id, state, outcome, skip_reason)
		SELECT id, $1, 'done', 'skipped', 'cancelled' FROM checks WHERE project_id = $2`, seed, p.ID)
	want := checkIDsNewestFirst(120)
	path := "/pg/" + itoa(seed)

	got, pages := s.PagedRows(path+"?per=50", "#history", 50)
	if pages != 3 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%d pages of history %v, want 3 pages of %v", pages, got, want)
	}
	d := s.Page(s.Get(path + "?page=2"))
	if txt := pagerText(d); !strings.Contains(txt, "Page 2 of 3") || !strings.Contains(txt, "Rows 51–100 of 120") {
		t.Errorf("pager = %q", txt)
	}
	if got := firstCells(s.Page(s.Get(path+"?per=500")), "#history"); len(got) != 120 {
		t.Errorf("per=500: %d rows", len(got))
	}
	if one := s.Page(s.Get(path + "?per=500")); one.Find(".pager .pages").Length() != 0 || one.Find(".pager .per").Length() == 0 {
		t.Error("single page: Prev/Next shown or the rows-per-page selector missing")
	}
}

func TestThePagePoolPaginatesTheFilteredPoolAndKeepsTheFilter(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	// 120 discovered Pages after the seed; the even ones are orphaned.
	s.Exec(`INSERT INTO pages (project_id, url, origin, orphan_streak)
		SELECT $1, 'https://shop.test/p' || lpad(n::text, 3, '0'), 'discovered', CASE WHEN n % 2 = 0 THEN 1 ELSE 0 END
		FROM generate_series(1, 120) AS n`, p.ID)
	all := []string{"https://shop.test/"}
	var orphaned []string
	for n := 1; n <= 120; n++ {
		u := fmt.Sprintf("https://shop.test/p%03d", n)
		all = append(all, u)
		if n%2 == 0 {
			orphaned = append(orphaned, u)
		}
	}
	base := "/p/" + itoa(p.ID) + "/pages"

	got, pages := s.PagedRows(base, "#pages", 50)
	if pages != 3 || strings.Join(got, ",") != strings.Join(all, ",") {
		t.Errorf("%d pages of the pool %v, want 3 pages of %v", pages, got, all)
	}
	got, pages = s.PagedRows(base+"?state=orphaned", "#pages", 50)
	if pages != 2 || strings.Join(got, ",") != strings.Join(orphaned, ",") {
		t.Errorf("%d pages of orphaned %v, want 2 pages of %v", pages, got, orphaned)
	}

	d := s.Page(s.Get(base + "?state=orphaned&per=50&page=2"))
	if txt := pagerText(d); !strings.Contains(txt, "Page 2 of 2") || !strings.Contains(txt, "Rows 51–60 of 60") {
		t.Errorf("orphaned pager = %q", txt)
	}
	filters := strings.Join(strings.Fields(d.Find("nav.filters").Text()), " ")
	if !strings.Contains(filters, "All (121)") || !strings.Contains(filters, "Active (121)") || !strings.Contains(filters, "Orphaned (60)") {
		t.Errorf("filter counts = %q, want counts of the whole pool", filters)
	}
	if href, _ := d.Find(".pager a.prev").First().Attr("href"); !strings.Contains(href, "state=orphaned") {
		t.Errorf("Prev link %q drops the filter", href)
	}

	// The filter and the rows-per-page selector both land on page 1 and keep each other.
	d = s.Page(s.Get(base + "?state=orphaned&per=100&page=2"))
	var retired string
	d.Find("nav.filters a").Each(func(_ int, a *goquery.Selection) {
		if strings.HasPrefix(a.Text(), "Retired") {
			retired, _ = a.Attr("href")
		}
	})
	if !strings.Contains(retired, "per=100") || strings.Contains(retired, "page=") {
		t.Errorf("Retired filter link = %q, want page 1 keeping per", retired)
	}
	d = s.Page(s.Get(base + "?state=orphaned&page=2"))
	var size200 string
	d.Find(".pager .per a").Each(func(_ int, a *goquery.Selection) {
		if a.Text() == "200" {
			size200, _ = a.Attr("href")
		}
	})
	if !strings.Contains(size200, "state=orphaned") || strings.Contains(size200, "page=") {
		t.Errorf("200 rows link = %q, want page 1 keeping the filter", size200)
	}
}
