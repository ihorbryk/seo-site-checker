package systemtest_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

// A Project's first Check is Check #1 under the Project, whatever other
// Projects have run before it.
func TestAFreshProjectsFirstCheckIsCheck1(t *testing.T) {
	s := systemtest.New(t)
	elsewhere := testfixtures.Project(t, s.Pool, testfixtures.WithOwner(s.User))
	for range 3 {
		c := testfixtures.Check(t, s.Pool, elsewhere)
		s.Exec(`UPDATE checks SET state = 'cancelled' WHERE id = $1`, c.ID)
	}
	s.Site("shop.test").Set("/", fakesite.Response{Body: "<html></html>"})
	p := s.AddProject("shop.test")

	w := s.Post("/p/"+itoa(p.ID)+"/checks", url.Values{})
	if loc := w.Header().Get("HX-Redirect"); loc != "/p/"+itoa(p.ID)+"/c/1" {
		t.Fatalf("Run Check sent the browser to %q, want /p/%d/c/1", loc, p.ID)
	}
	report := s.WaitForCheck(s.CheckID(p, 1))
	if got := text(report, "h1"); !strings.HasPrefix(got, "Check #1") {
		t.Errorf("Report title %q, want Check #1", got)
	}
	if got := text(s.Page(s.Get("/p/"+itoa(p.ID))), "table tr:nth-child(2) td:first-child"); got != "#1" {
		t.Errorf("Checks list shows %q, want #1", got)
	}
	if got := text(s.Page(s.Get("/")), ".last-check a"); !strings.Contains(got, "#1") {
		t.Errorf("home page last Check %q, want #1", got)
	}
}

func TestACheckNumberTheProjectDoesNotHaveIs404(t *testing.T) {
	s := systemtest.New(t)
	other := s.NewUser()
	p := testfixtures.Project(t, s.Pool, testfixtures.WithOwner(s.User))
	testfixtures.Check(t, s.Pool, p)

	for _, path := range []string{"/c/2", "/c/2/progress", "/c/2/sections/changed", "/c/0", "/c/abc"} {
		full := "/p/" + itoa(p.ID) + path
		if w := s.Get(full); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", full, w.Code)
		}
	}
	if w := s.Post("/p/"+itoa(p.ID)+"/c/2/cancel", url.Values{}); w.Code != http.StatusNotFound {
		t.Errorf("cancel a missing Check: status %d, want 404", w.Code)
	}
	if w := s.Get("/p/" + itoa(p.ID) + "/c/1"); w.Code != http.StatusOK {
		t.Errorf("owner's Check #1: status %d, want 200", w.Code)
	}
	if w := other.Get("/p/" + itoa(p.ID) + "/c/1"); w.Code != http.StatusNotFound {
		t.Errorf("another User's Check: status %d, want 404", w.Code)
	}
}

// Old /c/{id} links send the owner to the Check under its Project.
func TestOldCheckLinksRedirectToTheCheckNumber(t *testing.T) {
	s := systemtest.New(t)
	other := s.NewUser()
	elsewhere := testfixtures.Project(t, s.Pool, testfixtures.WithOwner(s.User))
	testfixtures.Check(t, s.Pool, elsewhere)
	p := testfixtures.Project(t, s.Pool, testfixtures.WithOwner(s.User))
	c := testfixtures.Check(t, s.Pool, p)

	w := s.Get("/c/" + itoa(c.ID))
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/p/"+itoa(p.ID)+"/c/1" {
		t.Errorf("GET /c/%d: status %d, Location %q; want 301 to /p/%d/c/1", c.ID, w.Code, w.Header().Get("Location"), p.ID)
	}
	if w := other.Get("/c/" + itoa(c.ID)); w.Code != http.StatusNotFound {
		t.Errorf("another User's old Check link: status %d, want 404", w.Code)
	}
	for _, path := range []string{"/c/" + itoa(c.ID) + "/progress", "/c/" + itoa(c.ID) + "/sections/changed"} {
		if w := s.Get(path); w.Code != http.StatusNotFound {
			t.Errorf("removed route GET %s: status %d, want 404", path, w.Code)
		}
	}
}
