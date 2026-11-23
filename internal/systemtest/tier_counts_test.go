package systemtest_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

// The Report, the Checks tab and the home screen all state how many Pages of
// one Check changed critically and in SEO; they read the same stored counts
// and must agree.
func TestCriticalAndSEOAreTheSameNumberOnEveryScreen(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	p := s.AddProject("shop.test")
	for _, path := range []string{"/a", "/b", "/c", "/d"} {
		testfixtures.Page(t, s.Pool, p, path)
	}
	for _, path := range []string{"/", "/a", "/b", "/c", "/d"} {
		site.Set(path, diffPage(pageA))
	}
	s.WaitForCheck(s.RunCheck(p))
	noindex := diffPage(strings.Replace(pageA, "<head>", `<head><meta name="robots" content="noindex">`, 1))
	retitled := diffPage(strings.Replace(pageA, "<title>Shop", "<title>Other", 1))
	site.Set("/", noindex)
	site.Set("/a", noindex)
	site.Set("/b", retitled)
	site.Set("/c", retitled)
	site.Set("/d", diffPage(strings.Replace(pageA, "hello", "bye", 1)))
	report := s.WaitForCheck(s.RunCheck(p))

	inReport := map[string]int{}
	for _, row := range changedRows(report) {
		inReport[row[strings.LastIndex(row, "|")+1:]]++
	}
	if inReport["critical"] != 2 || inReport["seo"] != 2 {
		t.Fatalf("Report rows by tier = %v, want 2 critical and 2 seo", inReport)
	}
	want := []string{"2", "2"}
	tab := s.Page(s.Get("/p/" + itoa(p.ID))).Find("#checks tr").Eq(1)
	got := []string{strings.TrimSpace(tab.Find(".c-critical").First().Text()), strings.TrimSpace(tab.Find(".c-seo").First().Text())}
	if !slices.Equal(got, want) {
		t.Errorf("Checks tab critical, SEO = %v, want %v", got, want)
	}
	home := s.Page(s.Get("/"))
	if got := []string{text(home, ".r-critical"), text(home, ".r-seo")}; !slices.Equal(got, want) {
		t.Errorf("home critical, SEO = %v, want %v", got, want)
	}
}
