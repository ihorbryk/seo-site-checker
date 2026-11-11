package systemtest_test

import (
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func TestScheduledCheckRunsWithoutAnyoneStartingIt(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").Set("/", fakesite.Response{Body: "<html><title>Shop</title></html>"})
	p := s.AddProject("shop.test")
	s.Exec(`UPDATE projects SET schedule = 'hourly' WHERE id = $1`, p.ID)
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)

	ids := s.Tick(now)
	if len(ids) != 1 {
		t.Fatalf("tick started %d Checks, want 1", len(ids))
	}
	report := s.WaitForCheck(ids[0])

	if got := text(report, "#report-header tr:contains(Trigger) td"); got != "scheduled" || text(report, ".fetched") != "1" {
		t.Errorf("trigger %q fetched %q", got, text(report, ".fetched"))
	}
	if got := s.Tick(now.Add(30 * time.Minute)); len(got) != 0 {
		t.Errorf("not yet due, but started %v", got)
	}
}
