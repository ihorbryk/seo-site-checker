package systemtest_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func TestSettingsEditedDuringACheckApplyFromTheNextCheck(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", fakesite.Response{Body: "<html>home</html>"})
	site.Set("/a", fakesite.Response{Body: "<html>a</html>"})
	p := s.AddProject("shop.test")
	if _, err := s.Q.CreatePage(context.Background(), dbgen.CreatePageParams{ProjectID: p.ID, Url: "https://shop.test/a", Origin: "manual"}); err != nil {
		t.Fatal(err)
	}
	// One lane, so /a is fetched only after the slow seed Page: after the edit.
	s.SaveRequestSettings(p, url.Values{
		"schedule": {"manual"}, "user_agent": {"OldBot/1"}, "timeout_s": {"30"}, "max_parallel": {"1"},
		"header_name": {"X-Env"}, "header_value": {"old"},
	})
	site.Fault("/", fakesite.Fault{Delay: 600 * time.Millisecond})

	first := s.RunCheck(p)
	waitFor(t, func() bool { return site.Hits("/") == 1 })
	s.SaveRequestSettings(p, url.Values{
		"schedule": {"manual"}, "user_agent": {"Googlebot/2.1"}, "timeout_s": {"30"}, "max_parallel": {"1"},
		"header_name": {"X-Env", "Authorization"}, "header_value": {"new", "Bearer t"},
	})
	s.WaitForCheck(first)

	for _, path := range []string{"/", "/a"} {
		got := site.Requests(path)
		if len(got) != 1 || got[0].Get("User-Agent") != "OldBot/1" || got[0].Get("X-Env") != "old" || got[0].Get("Authorization") != "" {
			t.Errorf("running Check sent new settings to %s: %v", path, got)
		}
	}

	s.WaitForCheck(s.RunCheck(p))
	got := site.Requests("/a")
	if len(got) != 2 {
		t.Fatalf("/a requests = %d", len(got))
	}
	if h := got[1]; h.Get("User-Agent") != "Googlebot/2.1" || h.Get("X-Env") != "new" || h.Get("Authorization") != "Bearer t" {
		t.Errorf("next Check did not use the new settings: %v", h)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(systemtest.WaitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
