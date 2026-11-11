package systemtest_test

import (
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// Spec rule: a Page whose Fetch failed is not counted as orphaned, so its
// streak never advances and it never retires, whatever the failure kind. A 5xx
// answer is a Snapshot, not a failure, so it counts like any other response.
func TestAnOrphanedPageWhoseFetchFailsNeverAdvancesItsStreak(t *testing.T) {
	cases := []struct {
		name    string
		breakIt func(site *fakesite.Site, s *systemtest.System, projectID int64)
		retires bool
	}{
		{"connection", func(site *fakesite.Site, _ *systemtest.System, _ int64) {
			site.Fault("/a", fakesite.Fault{Unreachable: true})
		}, false},
		{"truncated body", func(site *fakesite.Site, _ *systemtest.System, _ int64) {
			site.Fault("/a", fakesite.Fault{CloseMidResponse: true})
		}, false},
		{"timeout", func(site *fakesite.Site, s *systemtest.System, id int64) {
			s.Exec(`UPDATE projects SET timeout_s = 1 WHERE id = $1`, id)
			site.Fault("/a", fakesite.Fault{Delay: 3 * time.Second})
		}, false},
		{"body_too_large", func(site *fakesite.Site, _ *systemtest.System, _ int64) {
			site.OversizedBody("/a", 11<<20)
		}, false},
		{"5xx is a Snapshot, so it is counted", func(site *fakesite.Site, _ *systemtest.System, _ int64) {
			site.Set("/a", fakesite.Response{Status: 503, Body: "down"})
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := systemtest.New(t)
			site := s.Site("shop.test")
			site.Set("/", html(`<a href="/a">a</a>`))
			site.Set("/a", html(``))
			p := s.AddProject("shop.test")
			s.WaitForCheck(s.RunCheck(p))

			site.Set("/", html(``)) // /a is now orphaned
			tc.breakIt(site, s, p.ID)
			for i := 0; i < 5; i++ {
				s.WaitForCheck(s.RunCheck(p))
				streak := count(t, s, `SELECT orphan_streak FROM pages WHERE url = 'https://shop.test/a'`)
				if !tc.retires && streak != 0 {
					t.Fatalf("Check %d: orphan_streak = %d after a failed Fetch, want 0", i+1, streak)
				}
			}
			retired := count(t, s, `SELECT count(*) FROM pages WHERE url = 'https://shop.test/a' AND state = 'retired'`)
			if tc.retires && retired != 1 {
				t.Error("control: a 5xx orphan should retire after 3 orphaned Checks")
			}
			if !tc.retires && retired != 0 {
				t.Error("a Page retired though every Fetch of it failed")
			}
		})
	}
}
