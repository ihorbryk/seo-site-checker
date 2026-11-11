package systemtest_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func TestAVolatileCustomHeaderIsADiffUntilItIsIgnored(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", diffPage(pageA, "X-Build", "1"))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))

	site.Set("/", diffPage(pageA, "X-Build", "2"))
	second := s.WaitForCheck(s.RunCheck(p))
	if got := changedRows(second); len(got) != 1 || got[0] != "https://shop.test/|headers" {
		t.Fatalf("before ignoring: changed = %v", got)
	}

	s.SaveNoiseRules(p, url.Values{"header_names": {"x-build"}})
	site.Set("/", diffPage(pageA, "X-Build", "3"))
	third := s.WaitForCheck(s.RunCheck(p))
	if got := changedRows(third); len(got) != 0 {
		t.Errorf("after ignoring: changed = %v", got)
	}
	// The Diff page uses the same rules and names the Check they came from.
	d := s.Page(s.Get("/d/" + itoa(latestSnapshot(t, s))))
	if d.Find("#empty-diff").Length() != 1 {
		t.Errorf("Diff page is not empty")
	}
	if got := text(d, "#noise-source"); !strings.Contains(got, "Noise rules as of Check #3") {
		t.Errorf("noise source = %q", got)
	}
}

func TestABodyMaskSilencesAChangingTimestamp(t *testing.T) {
	stamped := func(ts string) string {
		return strings.Replace(pageA, "<p>hello</p>", "<p>hello</p><p>Rendered at "+ts+"</p>", 1)
	}
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", diffPage(stamped("10:00:01")))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", diffPage(stamped("10:00:02")))
	if got := changedRows(s.WaitForCheck(s.RunCheck(p))); len(got) != 1 || got[0] != "https://shop.test/|body" {
		t.Fatalf("before masking: changed = %v", got)
	}

	s.SaveNoiseRules(p, url.Values{"body_masks": {`Rendered at \d\d:\d\d:\d\d`}})
	site.Set("/", diffPage(stamped("10:00:03")))
	if got := changedRows(s.WaitForCheck(s.RunCheck(p))); len(got) != 0 {
		t.Errorf("after masking: changed = %v", got)
	}
}

func TestNoiseRulesNeverApplyToSEOFields(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", diffPage(pageA))
	p := s.AddProject("shop.test")
	s.SaveNoiseRules(p, url.Values{"body_masks": {`Shop|Store`}, "header_names": {"title"}})
	s.WaitForCheck(s.RunCheck(p))
	site.Set("/", diffPage(strings.Replace(pageA, "<title>Shop</title>", "<title>Store</title>", 1)))
	if got := changedRows(s.WaitForCheck(s.RunCheck(p))); len(got) != 1 || got[0] != "https://shop.test/|seo" {
		t.Errorf("changed = %v", got)
	}
}

func TestEditingNoiseRulesDuringACheckDoesNotAffectThatCheck(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", diffPage(pageA, "X-Build", "1"))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))

	site.Set("/", diffPage(pageA, "X-Build", "2"))
	site.Fault("/", fakesite.Fault{Delay: 500 * time.Millisecond})
	running := s.RunCheck(p)
	s.WaitFor(func() bool { return site.Hits("/") == 2 })
	s.SaveNoiseRules(p, url.Values{"header_names": {"x-build"}})
	if got := changedRows(s.WaitForCheck(running)); len(got) != 1 {
		t.Errorf("running Check used the edited rules: changed = %v", got)
	}
}

func TestAnInvalidRegexIsRefusedWithAMessage(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	w := s.Post("/p/"+itoa(p.ID)+"/settings/noise", url.Values{"header_names": {"x-build"}, "body_masks": {"ok\\d\n(unclosed"}})
	if w.Code != 422 {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, "(unclosed") || !strings.Contains(got, "RE2") {
		t.Errorf("message = %q", got)
	}
	d := s.Page(s.Get("/p/" + itoa(p.ID) + "/settings"))
	if v := d.Find(`textarea[name="header_names"]`).Text(); strings.Contains(v, "x-build") {
		t.Errorf("refused rules were saved: %q", v)
	}
}

func TestTheSettingsTabListsBuiltInRulesReadOnlyAndSavedProjectRules(t *testing.T) {
	s := systemtest.New(t)
	p := s.AddProject("shop.test")
	s.SaveNoiseRules(p, url.Values{"header_names": {"X-Build\nX-Env"}, "body_masks": {`id-\d+`}})
	d := s.Page(s.Get("/p/" + itoa(p.ID) + "/settings"))
	if d.Find(`details#builtin-rules`).Length() != 1 || !strings.Contains(text(d, "#builtin-rules"), "Set-Cookie") {
		t.Error("built-in rules collapsible missing")
	}
	if got := strings.TrimSpace(d.Find(`form#noise-rules textarea[name="header_names"]`).Text()); got != "X-Build\nX-Env" {
		t.Errorf("header names = %q", got)
	}
	if got := strings.TrimSpace(d.Find(`form#noise-rules textarea[name="body_masks"]`).Text()); got != `id-\d+` {
		t.Errorf("body masks = %q", got)
	}
}

func latestSnapshot(t *testing.T, s *systemtest.System) int64 {
	t.Helper()
	var id int64
	if err := s.Pool.QueryRow(t.Context(), `SELECT max(id) FROM snapshots`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
