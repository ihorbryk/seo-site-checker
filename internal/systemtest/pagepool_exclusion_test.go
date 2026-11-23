package systemtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// addPool puts n discovered Pages /p/1 ... /p/n into the Project's pool.
func addPool(t *testing.T, s *systemtest.System, domain string, projectID int64, n int) {
	t.Helper()
	s.Exec(`INSERT INTO pages (project_id, url, origin)
		SELECT $1, 'https://' || $3::text || '/p/' || g, 'discovered' FROM generate_series(1, $2::int) g`, projectID, n, domain)
}

// filterCounts returns the six filter counts the tab shows, in display order.
func filterCounts(t *testing.T, s *systemtest.System, pid int64) string {
	t.Helper()
	var parts []string
	s.Page(s.Get("/p/" + itoa(pid) + "/pages")).Find("nav.filters > *").Each(func(_ int, a *goquery.Selection) {
		parts = append(parts, strings.Join(strings.Fields(a.Text()), " "))
	})
	return strings.Join(parts, ",")
}

// After patterns are saved and the jobs finish, the counts and lists follow
// the new patterns, for a pool larger than one batch.
func TestSavingExcludePatternsRecomputesAPoolLargerThanOneBatch(t *testing.T) {
	s := systemtest.New(t, systemtest.ExcludeBatchSize(4))
	p := s.AddProject("shop.test")
	addPool(t, s, "shop.test", p.ID, 14) // + the seed Page: 15 Pages, 4 batches

	s.SaveRequestSettings(p, settingsForm("/p/1*"))
	// /p/1 and /p/10 ... /p/14 match.
	s.WaitFor(func() bool { return len(filteredRows(t, s, p, "excluded")) == 6 })

	if got := filteredRows(t, s, p, "active"); len(got) != 9 {
		t.Errorf("%d active Pages, want 9: %q", len(got), got)
	}
	if got := filterCounts(t, s, p.ID); got != "All (15),Active (9),Orphaned (0),Retired (0),Ignored (0),Excluded (6)" {
		t.Errorf("counts %q", got)
	}

	// Patterns saved again: the Pages come back, the others go.
	s.SaveRequestSettings(p, settingsForm("/p/2*"))
	s.WaitFor(func() bool {
		got := filteredRows(t, s, p, "excluded")
		return len(got) == 1 && strings.HasSuffix(got[0], "/p/2")
	})
	if got := filteredRows(t, s, p, "active"); len(got) != 14 {
		t.Errorf("%d active Pages, want 14", len(got))
	}
}

// A Page added by hand that matches a pattern shows as excluded straight away,
// whatever the recompute has or has not reached.
func TestAManuallyAddedPageThatMatchesAPatternIsExcludedWithoutARecompute(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	s.SaveRequestSettings(p, settingsForm("/private/**"))
	s.WaitFor(func() bool {
		return count(t, s, `SELECT count(*) FROM river_job WHERE kind = 'exclude_recompute' AND state <> 'completed'`) == 0
	})

	if w := s.AddPage(p, "https://shop.test/private/x"); w.Code != 200 {
		t.Fatalf("add Page: status %d: %s", w.Code, w.Body.String())
	}
	if got := filteredRows(t, s, p, "excluded"); !sameSet(got, []string{"https://shop.test/private/x"}) {
		t.Errorf("excluded = %q", got)
	}
}

// A Page discovered during a Check is stored with the answer for the Project's
// current patterns, not the copy frozen onto the Check.
func TestADiscoveredPageGetsTheAnswerForTheCurrentPatterns(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/late/x">x</a>`))
	site.Set("/late/x", html(`x`))
	site.Fault("/", fakesite.Fault{Delay: 500 * time.Millisecond})
	p := s.AddProject("shop.test")

	c := s.RunCheck(p)
	s.WaitFor(func() bool {
		return count(t, s, `SELECT count(*) FROM checks WHERE id = $1 AND state = 'running'`, c) == 1
	})
	// Patterns change while the seed is being fetched; no recompute job is
	// queued by this direct update.
	s.Exec(`UPDATE projects SET exclude_patterns = '{"/late/*"}' WHERE id = $1`, p.ID)
	s.WaitForCheck(c)

	if got := filteredRows(t, s, p, "excluded"); !sameSet(got, []string{"https://shop.test/late/x"}) {
		t.Errorf("excluded = %q (all: %q)", got, poolRows(t, s, p))
	}
}
