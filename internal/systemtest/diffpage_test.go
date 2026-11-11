package systemtest_test

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
)

// diffLink follows the Changed Pages row of the Report to the Diff page.
func diffLink(t *testing.T, report *goquery.Document) string {
	t.Helper()
	href, ok := report.Find(`#changed a[href^="/d/"]`).First().Attr("href")
	if !ok {
		t.Fatal("Report row has no Diff link")
	}
	return href
}

func TestTheDiffPageShowsSEOHeadersAndASideBySideBodyDiffByDefault(t *testing.T) {
	s, _, second := twoChecks(t, diffPage(pageA, "X-Powered-By", "a", "Date", "x"), func(site *fakesite.Site) {
		site.Set("/", diffPage(strings.Replace(strings.Replace(pageA, "<title>Shop</title>", "<title>Store</title>", 1), "hello", "goodbye", 1),
			"X-Powered-By", "b", "X-New", "1", "Date", "y"))
	})
	href := diffLink(t, second)
	w := s.Get(href)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	d := s.Page(w)

	if got := strings.Join(strings.Fields(text(d, ".crumbs")), " "); !strings.HasPrefix(got, "Projects › shop.test › Checks › Check #2 › Diff /") {
		t.Errorf("breadcrumbs = %q", got)
	}
	if got := text(d, "#diff-seo"); !strings.Contains(got, "title") || !strings.Contains(got, "Shop") || !strings.Contains(got, "Store") {
		t.Errorf("SEO table = %q", got)
	}
	kinds := map[string]string{}
	d.Find("#diff-headers tr[class]").Each(func(_ int, tr *goquery.Selection) {
		kinds[strings.TrimSpace(tr.Find("th").Text())] = strings.TrimSpace(tr.Find(".badge").Text())
	})
	if len(kinds) != 2 || kinds["x-powered-by"] != "changed" || kinds["x-new"] != "added" {
		t.Errorf("header changes = %v", kinds)
	}
	if got := text(d, "#ignored-headers"); !strings.Contains(got, "date") {
		t.Errorf("ignored line = %q", got)
	}
	if d.Find("#body-diff[data-mode=split]").Length() != 1 || d.Find("#body-diff td.del").Length() == 0 || d.Find("#body-diff td.add").Length() == 0 {
		t.Errorf("split body diff missing: %s", w.Body.String())
	}
	if !strings.Contains(d.Find("#body-diff td.del").Text(), "hello") || !strings.Contains(d.Find("#body-diff td.add").Text(), "goodbye") {
		t.Errorf("body lines: del %q add %q", text(d, "#body-diff td.del"), text(d, "#body-diff td.add"))
	}
	if d.Find("#empty-diff").Length() != 0 {
		t.Error("non-empty Diff has the empty note")
	}
}

func TestTheUnifiedToggleSwapsInPlaceAsAFragment(t *testing.T) {
	s, _, second := twoChecks(t, diffPage(pageA), func(site *fakesite.Site) {
		site.Set("/", diffPage(strings.Replace(pageA, "hello", "goodbye", 1)))
	})
	href := diffLink(t, second)
	full := s.Page(s.Get(href))
	toggle, ok := full.Find(`#diff-toggle a[hx-get]`).Attr("hx-get")
	if !ok || !strings.HasSuffix(toggle, "?diff=unified") {
		t.Fatalf("toggle hx-get = %q", toggle)
	}
	if target, _ := full.Find(`#diff-toggle a[hx-get]`).Attr("hx-target"); target != "closest .diffbox" {
		t.Errorf("hx-target = %q", target)
	}

	w := s.GetHTMX(toggle)
	if strings.Contains(w.Body.String(), "<html") {
		t.Error("htmx response includes the page shell")
	}
	d := s.Page(w)
	if d.Find("#body-diff[data-mode=unified] table.unified").Length() != 1 {
		t.Fatalf("no unified view: %s", w.Body.String())
	}
	if !strings.Contains(d.Find("#body-diff tr.del").Text(), "hello") || !strings.Contains(d.Find("#body-diff tr.add").Text(), "goodbye") {
		t.Errorf("unified rows: del %q add %q", text(d, "#body-diff tr.del"), text(d, "#body-diff tr.add"))
	}
}

func TestADiffWithNothingLeftAfterNoiseShowsTheEmptyNote(t *testing.T) {
	nonce := func(n string) string {
		return strings.Replace(pageA, "<body>", `<body><script nonce="`+n+`">var a=1</script>`, 1)
	}
	s, _, _ := twoChecks(t, diffPage(nonce("aaa"), "Date", "Mon"), func(site *fakesite.Site) {
		site.Set("/", diffPage(nonce("bbb"), "Date", "Tue"))
	})
	ids := snapshotIDs(t, s)
	d := s.Page(s.Get("/d/" + itoa(ids[1])))
	if d.Find("#empty-diff").Length() != 1 {
		t.Error("no Empty Diff note")
	}
	if d.Find("#body-diff").Length() != 0 || d.Find("#diff-seo").Length() != 0 {
		t.Error("empty Diff still shows change sections")
	}
	if !strings.Contains(text(d, "#ignored-headers"), "date") {
		t.Errorf("ignored = %q", text(d, "#ignored-headers"))
	}
}

func TestABodyAppearingIsReportedAsBodyAdded(t *testing.T) {
	plain := fakesite.Response{Header: map[string][]string{"Content-Type": {"text/plain"}}, Body: "hi"}
	s, _, second := twoChecks(t, plain, func(site *fakesite.Site) {
		site.Set("/", diffPage(pageA, "Content-Type", "text/html"))
	})
	d := s.Page(s.Get(diffLink(t, second)))
	if got := text(d, "#body-note"); got != "body added" {
		t.Errorf("body note = %q", got)
	}
	if d.Find("#body-diff").Length() != 0 {
		t.Error("line diff shown for a one-sided body")
	}
}

func TestABodyDisappearingIsReportedAsBodyRemoved(t *testing.T) {
	plain := fakesite.Response{Header: map[string][]string{"Content-Type": {"text/plain"}}, Body: "hi"}
	s, _, second := twoChecks(t, diffPage(pageA, "Content-Type", "text/html"), func(site *fakesite.Site) {
		site.Set("/", plain)
	})
	d := s.Page(s.Get(diffLink(t, second)))
	if got := text(d, "#body-note"); got != "body removed" {
		t.Errorf("body note = %q", got)
	}
}

func TestAFirstSnapshotHasADiffPageSayingSo(t *testing.T) {
	s := systemtest.New(t)
	s.Site("shop.test").Set("/", diffPage(pageA))
	s.WaitForCheck(s.RunCheck(s.AddProject("shop.test")))
	ids := snapshotIDs(t, s)
	d := s.Page(s.Get("/d/" + itoa(ids[0])))
	if d.Find("#first-snapshot").Length() != 1 {
		t.Error("no first-Snapshot note")
	}
	if s.Get("/d/999999").Code != 404 {
		t.Error("unknown Snapshot is not a 404")
	}
}

func TestTheSnapshotPageLinksToItsDiffVsPrevious(t *testing.T) {
	s, _, _ := twoChecks(t, diffPage(pageA), func(site *fakesite.Site) {
		site.Set("/", diffPage(strings.Replace(pageA, "hello", "goodbye", 1)))
	})
	ids := snapshotIDs(t, s)
	if s.Page(s.Get("/s/"+itoa(ids[0]))).Find("a#diff-link").Length() != 0 {
		t.Error("first Snapshot links to a Diff")
	}
	href, _ := s.Page(s.Get("/s/" + itoa(ids[1]))).Find("a#diff-link").Attr("href")
	if href != "/d/"+itoa(ids[1]) {
		t.Errorf("Diff link = %q", href)
	}
}
