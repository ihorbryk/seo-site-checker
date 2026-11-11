package systemtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func TestCancellingMidCheckKeepsInFlightSnapshotsSkipsTheRestAndWritesNoOrphanEvents(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a><a href="/d">d</a>`))
	for _, path := range []string{"/a", "/b", "/c", "/d"} {
		site.Set(path, html(path))
	}
	p := s.AddProject("shop.test")
	s.Exec(`UPDATE projects SET max_parallel = 1 WHERE id = $1`, p.ID)
	first := s.WaitForCheck(s.RunCheck(p))
	if first.Find("#partial-banner").Length() != 0 {
		t.Error("partial banner on a finished Check")
	}

	site.Set("/", html(`<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`)) // /d would vanish
	site.Set("/a", html("a changed"))
	site.Fault("/a", fakesite.Fault{Delay: 3 * time.Second})
	hitsBefore := site.Hits("/a")
	id := s.RunCheck(p)
	s.WaitFor(func() bool { return site.Hits("/a") > hitsBefore })
	s.Cancel(id)
	if got := s.Page(s.Get("/c/" + itoa(id))).Find("#progress").Text(); !strings.Contains(got, "Cancelling") {
		t.Errorf("progress while cancelling: %q", got)
	}
	report := s.WaitForCheck(id)

	if text(report, "#partial-banner") == "" {
		t.Error("no partial banner on the cancelled Report")
	}
	if text(report, "h1 .badge") != "cancelled" {
		t.Errorf("state %q", text(report, "h1 .badge"))
	}
	if text(report, ".fetched") != "2" || text(report, ".skipped") != "3" {
		t.Errorf("fetched %q skipped %q, want 2 and 3", text(report, ".fetched"), text(report, ".skipped"))
	}
	if n := count(t, s, `SELECT count(*) FROM fetches WHERE check_id = $1 AND outcome = 'skipped' AND skip_reason = 'cancelled' AND state = 'done'`, id); n != 3 {
		t.Errorf("%d cancelled skips, want 3", n)
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots sn JOIN pages pg ON pg.id = sn.page_id WHERE sn.check_id = $1 AND pg.url = 'https://shop.test/a'`, id); n != 1 {
		t.Errorf("in-flight Page has %d Snapshots, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM page_events WHERE check_id = $1 AND kind IN ('vanished', 'retired')`, id); n != 0 {
		t.Errorf("%d vanished/retired events in a cancelled Check", n)
	}
	if n := count(t, s, `SELECT count(*) FROM pages WHERE orphan_streak > 0 OR state <> 'active'`); n != 0 {
		t.Errorf("%d Pages had their orphan state changed", n)
	}
	checks := s.Page(s.Get("/p/" + itoa(p.ID)))
	if checks.Find("#checks .badge.partial").Length() != 1 {
		t.Error("Checks table has no partial badge")
	}

	// The next Check starts normally and sees /d vanish against the kept Snapshots.
	site.Fault("/a", fakesite.Fault{})
	next := s.WaitForCheck(s.RunCheck(p))
	if got := section(next, "vanished"); !sameSet(got, []string{"https://shop.test/d"}) {
		t.Errorf("vanished in the next Check = %q", got)
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots sn JOIN pages pg ON pg.id = sn.page_id WHERE pg.url = 'https://shop.test/b'`); n != 2 {
		t.Errorf("/b has %d Snapshots, want 2 (first and next Check, none from the cancelled one)", n)
	}
}

func TestACheckCanBeCancelledOnlyWhileActive(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(``))
	p := s.AddProject("shop.test")
	id := s.RunCheck(p)
	s.WaitForCheck(id)
	s.Cancel(id) // already finished: a no-op
	if n := count(t, s, `SELECT count(*) FROM checks WHERE id = $1 AND state = 'finished'`, id); n != 1 {
		t.Error("cancelling a finished Check changed it")
	}
}
