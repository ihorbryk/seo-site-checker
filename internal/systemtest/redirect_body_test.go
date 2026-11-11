package systemtest_test

import (
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func TestARedirectWithAnOversizedBodyIsStillARedirectSnapshot(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	big := redirectTo(301, "/b")
	big.Body = strings.Repeat("a", 10<<20+1)
	site.Set("/a", big)
	site.Set("/b", html(`end`))

	report := s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))

	if got := failureRows(report); len(got) != 0 {
		t.Errorf("failures = %v, want none: a 3xx body is never read", got)
	}
	if hits := site.Hits("/b"); hits != 1 {
		t.Errorf("redirect target fetched %d times, want 1", hits)
	}
}
