package systemtest_test

import (
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func TestADiscoveredPageIsNewOnlyOnceItGetsItsFirstSnapshot(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(`a`))
	site.Fault("/a", fakesite.Fault{Unreachable: true})
	p := s.AddProject("shop.test")

	first := s.WaitForCheck(s.RunCheck(p))
	if got := section(first, "new"); len(got) != 0 {
		t.Errorf("a Page whose Fetch failed is new: %q", got)
	}
	if text(first, ".new-pages") != "0" {
		t.Errorf("new count %q, want 0", text(first, ".new-pages"))
	}

	site.Fault("/a", fakesite.Fault{})
	second := s.WaitForCheck(s.RunCheck(p))
	if got := section(second, "new"); !sameSet(got, []string{"https://shop.test/a"}) {
		t.Errorf("new in the Check that first fetched /a = %q", got)
	}
}

func TestAManualPagesFirstSnapshotIsNewAndItVanishesButNeverRetires(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(``))
	site.Set("/m", html(`m`))
	p := s.AddProject("shop.test")
	if w := s.AddPage(p, "https://shop.test/m"); w.Code >= 400 {
		t.Fatalf("add page: %d", w.Code)
	}

	first := s.WaitForCheck(s.RunCheck(p))
	if got := section(first, "new"); !sameSet(got, []string{"https://shop.test/m"}) {
		t.Errorf("new = %q, want the manual Page", got)
	}
	if got := section(first, "vanished"); !sameSet(got, []string{"https://shop.test/m"}) {
		t.Errorf("an orphaned manual Page should vanish: %q", got)
	}
	for i := 0; i < 4; i++ {
		report := s.WaitForCheck(s.RunCheck(p))
		if got := section(report, "retired"); len(got) != 0 {
			t.Fatalf("a manual Page retired: %q", got)
		}
		if got := section(report, "vanished"); len(got) != 0 {
			t.Fatalf("a manual Page vanished again: %q", got)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE origin = 'manual' AND state = 'active'`); n != 1 {
		t.Errorf("manual Page is not active (%d)", n)
	}
}

func TestAnOrphanedPageWhoseFetchFailsNeverRetires(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", html(``))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", html(``))
	site.Fault("/a", fakesite.Fault{Unreachable: true})

	for i := 0; i < 5; i++ {
		report := s.WaitForCheck(s.RunCheck(p))
		if len(section(report, "vanished"))+len(section(report, "retired")) != 0 {
			t.Fatalf("Check %d: orphan events for a Page whose Fetch failed", i+1)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE url = 'https://shop.test/a' AND state = 'active' AND orphan_streak = 0`); n != 1 {
		t.Error("the failing Page's orphan streak moved or it retired")
	}
}

func TestACancelledCheckStillListsPagesThatGotTheirFirstSnapshot(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a>`))
	site.Set("/a", html(`a`))
	site.Set("/b", html(`b`))
	site.Fault("/a", fakesite.Fault{Delay: 3 * time.Second})
	p := s.AddProject("shop.test")
	s.Exec(`UPDATE projects SET max_parallel = 1 WHERE id = $1`, p.ID)

	id := s.RunCheck(p)
	waitFor(t, func() bool { return site.Hits("/a") > 0 })
	s.Cancel(id)
	report := s.WaitForCheck(id)

	if got := section(report, "new"); !sameSet(got, []string{"https://shop.test/a"}) {
		t.Errorf("new in the cancelled Check = %q, want only the Page that got a Snapshot", got)
	}
	if n := count(t, s, `SELECT count(*) FROM page_events WHERE check_id = $1 AND kind = 'new'`, id); n != 1 {
		t.Errorf("%d new events, want 1", n)
	}
}
