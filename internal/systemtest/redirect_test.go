package systemtest_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

func redirectTo(status int, location string) fakesite.Response {
	return fakesite.Response{Status: status, Header: http.Header{"Location": {location}}, Body: "<html><body><a href=\"/ignored\">moved</a></body></html>"}
}

func TestAnOnDomainRedirectChainIsFetchedInOneCheck(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", redirectTo(301, "/b"))
	site.Set("/b", redirectTo(302, "https://shop.test/c"))
	site.Set("/c", html(`end`))
	site.Set("/ignored", html(`must not be discovered from a 3xx body`))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	for _, path := range []string{"/", "/a", "/b", "/c"} {
		if hits := site.Hits(path); hits != 1 {
			t.Errorf("%s fetched %d times, want 1", path, hits)
		}
	}
	if hits := site.Hits("/ignored"); hits != 0 {
		t.Errorf("a 3xx body was parsed: /ignored fetched %d times", hits)
	}
	if text(report, ".new-pages") != "3" {
		t.Errorf("new Pages %q, want 3 (/a, /b, /c)", text(report, ".new-pages"))
	}
	want := []string{
		"https://shop.test/|active|200", "https://shop.test/a|active|301",
		"https://shop.test/b|active|302", "https://shop.test/c|active|200",
	}
	if got := poolRows(t, s, p); !sameSet(got, want) {
		t.Errorf("Page pool = %q, want %q", got, want)
	}
	// Each 3xx Snapshot has exactly one outgoing link, from source `redirect`.
	if n := count(t, s, `SELECT count(*) FROM links WHERE source = 'redirect'`); n != 2 {
		t.Errorf("%d redirect links, want 2", n)
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots s JOIN pages p ON p.id = s.page_id
		WHERE p.url = 'https://shop.test/a' AND s.seo_fields->'redirect_target' = '["https://shop.test/b"]'
		AND s.body_sha256 IS NULL`); n != 1 {
		t.Errorf("/a Snapshot should have redirect_target /b and no body, found %d", n)
	}
	if n := count(t, s, `SELECT count(*) FROM links l JOIN snapshots s ON s.id = l.snapshot_id JOIN pages p ON p.id = s.page_id WHERE p.url = 'https://shop.test/a'`); n != 1 {
		t.Errorf("/a has %d outgoing links, want exactly 1", n)
	}
}

func TestARedirectLoopTerminates(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", redirectTo(301, "/b"))
	site.Set("/b", redirectTo(301, "/a"))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/a") != 1 || site.Hits("/b") != 1 {
		t.Errorf("hits /a=%d /b=%d, want 1 each", site.Hits("/a"), site.Hits("/b"))
	}
	if text(report, ".fetched") != "3" {
		t.Errorf("fetched %q, want 3", text(report, ".fetched"))
	}
}

func TestAnOffDomainRedirectTargetIsRecordedButNotFetched(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	other := s.Site("other.test")
	other.Set("/", html(`elsewhere`))
	site.Set("/", html(`<a href="/a">a</a><a href="/h">h</a>`))
	site.Set("/a", redirectTo(301, "https://other.test/"))
	site.Set("/h", redirectTo(301, "http://shop.test/plain"))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if other.Hits("/") != 0 {
		t.Error("an off-domain redirect target was fetched")
	}
	if site.Hits("/plain") != 0 {
		t.Error("an http redirect target was fetched")
	}
	if text(report, ".fetched") != "3" {
		t.Errorf("fetched %q, want 3", text(report, ".fetched"))
	}
	if n := count(t, s, `SELECT count(*) FROM links WHERE source = 'redirect'`); n != 0 {
		t.Errorf("%d redirect links, want 0", n)
	}
	for url, target := range map[string]string{"https://shop.test/a": "https://other.test/", "https://shop.test/h": "http://shop.test/plain"} {
		if n := count(t, s, `SELECT count(*) FROM snapshots s JOIN pages p ON p.id = s.page_id
			WHERE p.url = $1 AND s.seo_fields->'redirect_target' = to_jsonb(ARRAY[$2::text])`, url, target); n != 1 {
			t.Errorf("%s: redirect_target %s not recorded", url, target)
		}
	}
}

func TestASelfRedirectIsANormal3xxSnapshot(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/s">s</a>`))
	site.Set("/s", redirectTo(301, "/s"))
	p := s.AddProject("shop.test")

	report := s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/s") != 1 {
		t.Errorf("/s fetched %d times, want 1", site.Hits("/s"))
	}
	if text(report, ".fetched") != "2" {
		t.Errorf("fetched %q, want 2", text(report, ".fetched"))
	}
	if n := count(t, s, `SELECT count(*) FROM snapshots WHERE status = 301`); n != 1 {
		t.Errorf("%d 301 Snapshots, want 1", n)
	}
}

func TestARedirectTargetKeepsItsPageFromBeingOrphaned(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", redirectTo(301, "/b"))
	site.Set("/b", html(`target`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))

	second := s.WaitForCheck(s.RunCheck(p))

	if got := section(second, "vanished"); len(got) != 0 {
		t.Errorf("vanished = %q; /b is linked by /a's redirect", got)
	}
}

func TestARedirectTargetExcludedByPatternIsNotFetched(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", redirectTo(301, "/private/x"))
	site.Set("/private/x", html(`secret`))
	p := s.AddProject("shop.test")
	s.SaveRequestSettings(p, settingsForm("/private/**\n"))

	s.WaitForCheck(s.RunCheck(p))

	if site.Hits("/private/x") != 0 {
		t.Error("an excluded redirect target was fetched")
	}
}

func TestARedirectTargetChangeRanksCriticalAndShowsTheChain(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", redirectTo(301, "/b"))
	site.Set("/b", html(`b`))
	site.Set("/c", redirectTo(301, "/b"))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))

	site.Set("/a", redirectTo(301, "/c"))
	report := s.WaitForCheck(s.RunCheck(p))

	got := changedRows(report)
	if !sameSet(got, []string{"https://shop.test/a|critical"}) {
		t.Fatalf("changed = %q", got)
	}
	if seo := text(report, "#changed .seo"); !strings.Contains(seo, "redirect_target") || !strings.Contains(seo, "https://shop.test/b → https://shop.test/c") {
		t.Errorf("SEO column = %q", seo)
	}
	if chain := text(report, "#changed .chain"); chain != "https://shop.test/a → https://shop.test/c → https://shop.test/b" {
		t.Errorf("chain = %q", chain)
	}
}

func TestAChainRowMarksALoop(t *testing.T) {
	s := systemtest.New(t)
	site := s.Site("shop.test")
	site.Set("/", html(`<a href="/a">a</a>`))
	site.Set("/a", redirectTo(301, "/b"))
	site.Set("/b", redirectTo(301, "/x"))
	site.Set("/x", html(`x`))
	p := s.AddProject("shop.test")
	s.WaitForCheck(s.RunCheck(p))

	site.Set("/b", redirectTo(301, "/a"))
	report := s.WaitForCheck(s.RunCheck(p))

	if got := changedRows(report); !sameSet(got, []string{"https://shop.test/b|critical"}) {
		t.Fatalf("changed = %q", got)
	}
	if chain := text(report, "#changed .chain"); chain != "https://shop.test/b → https://shop.test/a → https://shop.test/b (loop)" {
		t.Errorf("chain = %q", chain)
	}
}
