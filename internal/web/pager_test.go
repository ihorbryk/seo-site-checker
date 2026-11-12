package web

import (
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPaginateParsesLenientlyAndClamps(t *testing.T) {
	for _, tc := range []struct {
		query                string
		total                int
		page, per, last, off int
		rows                 string
	}{
		{"", 1234, 1, 50, 25, 0, "Rows 1–50 of 1,234"},
		{"page=3", 1234, 3, 50, 25, 100, "Rows 101–150 of 1,234"},
		{"page=25", 1234, 25, 50, 25, 1200, "Rows 1,201–1,234 of 1,234"},
		{"page=99", 1234, 25, 50, 25, 1200, "Rows 1,201–1,234 of 1,234"},
		{"page=0", 1234, 1, 50, 25, 0, "Rows 1–50 of 1,234"},
		{"page=-3", 1234, 1, 50, 25, 0, "Rows 1–50 of 1,234"},
		{"page=abc", 1234, 1, 50, 25, 0, "Rows 1–50 of 1,234"},
		{"per=100&page=2", 1234, 2, 100, 13, 100, "Rows 101–200 of 1,234"},
		{"per=200", 1234, 1, 200, 7, 0, "Rows 1–200 of 1,234"},
		{"per=500", 1234, 1, 500, 3, 0, "Rows 1–500 of 1,234"},
		{"per=75", 1234, 1, 50, 25, 0, "Rows 1–50 of 1,234"},
		{"per=x", 10, 1, 50, 1, 0, "Rows 1–10 of 10"},
		{"page=4", 0, 1, 50, 1, 0, "No rows"},
		{"", 50, 1, 50, 1, 0, "Rows 1–50 of 50"},
		{"page=2", 51, 2, 50, 2, 50, "Rows 51–51 of 51"},
	} {
		p := paginate(mustURL(t, "/x?"+tc.query), tc.total)
		if p.Page != tc.page || p.Per != tc.per || p.Last != tc.last || p.Offset() != tc.off || p.Rows != tc.rows {
			t.Errorf("?%s total %d: page %d per %d last %d offset %d rows %q, want %d %d %d %d %q",
				tc.query, tc.total, p.Page, p.Per, p.Last, p.Offset(), p.Rows, tc.page, tc.per, tc.last, tc.off, tc.rows)
		}
	}
}

func TestPaginateLinksKeepOtherParameters(t *testing.T) {
	p := paginate(mustURL(t, "/p/1/pages?state=orphaned&per=100&page=2"), 450)
	if want := "/p/1/pages?page=1&per=100&state=orphaned"; p.PrevHref != want {
		t.Errorf("prev = %q, want %q", p.PrevHref, want)
	}
	if want := "/p/1/pages?page=3&per=100&state=orphaned"; p.NextHref != want {
		t.Errorf("next = %q, want %q", p.NextHref, want)
	}
	var sizes []string
	for _, s := range p.Sizes {
		if s.Current != (s.Per == 100) {
			t.Errorf("size %d current = %v", s.Per, s.Current)
		}
		sizes = append(sizes, s.Href)
	}
	want := []string{
		"/p/1/pages?per=50&state=orphaned", "/p/1/pages?per=100&state=orphaned",
		"/p/1/pages?per=200&state=orphaned", "/p/1/pages?per=500&state=orphaned",
	}
	if len(sizes) != len(want) {
		t.Fatalf("sizes = %q", sizes)
	}
	for i := range want {
		if sizes[i] != want[i] {
			t.Errorf("size link %d = %q, want %q", i, sizes[i], want[i])
		}
	}
	if got, want := p.With("state", "retired"), "/p/1/pages?per=100&state=retired"; got != want {
		t.Errorf("With(state, retired) = %q, want %q", got, want)
	}
	if got, want := p.With("state", ""), "/p/1/pages?per=100"; got != want {
		t.Errorf("With(state, \"\") = %q, want %q", got, want)
	}
}

func TestPaginateDisablesPrevOnFirstAndNextOnLast(t *testing.T) {
	first := paginate(mustURL(t, "/c"), 120)
	if first.PrevHref != "" || first.NextHref == "" || !first.Multi {
		t.Errorf("first page: prev %q next %q multi %v", first.PrevHref, first.NextHref, first.Multi)
	}
	last := paginate(mustURL(t, "/c?page=3"), 120)
	if last.PrevHref == "" || last.NextHref != "" {
		t.Errorf("last page: prev %q next %q", last.PrevHref, last.NextHref)
	}
	single := paginate(mustURL(t, "/c?page=2"), 20)
	if single.Multi || single.Page != 1 || len(single.Sizes) != 4 {
		t.Errorf("single page: multi %v page %d sizes %d", single.Multi, single.Page, len(single.Sizes))
	}
}

func TestSectionPagerUsesPrefixedParametersAndKeepsTheOthers(t *testing.T) {
	u := mustURL(t, "/c/7?show=skipped&changed_page=2&vanished_page=3&vanished_per=100")
	p := paginateSection(u, "vanished", 450)
	if p.Page != 3 || p.Per != 100 || p.Rows != "Rows 201–300 of 450" {
		t.Fatalf("page %d per %d rows %q", p.Page, p.Per, p.Rows)
	}
	if want := "/c/7?changed_page=2&show=skipped&vanished_page=2&vanished_per=100#vanished"; p.PrevHref != want {
		t.Errorf("prev = %q, want %q", p.PrevHref, want)
	}
	if want := "/c/7/sections/vanished?changed_page=2&show=skipped&vanished_page=2&vanished_per=100"; p.PrevHx != want {
		t.Errorf("prev hx = %q, want %q", p.PrevHx, want)
	}
	if want := "/c/7?changed_page=2&show=skipped&vanished_page=4&vanished_per=100#vanished"; p.NextHref != want {
		t.Errorf("next = %q, want %q", p.NextHref, want)
	}
	if want := "/c/7?changed_page=2&show=skipped&vanished_per=200#vanished"; p.Sizes[2].Href != want {
		t.Errorf("size 200 = %q, want %q", p.Sizes[2].Href, want)
	}
	if want := "/c/7/sections/vanished?changed_page=2&show=skipped&vanished_per=200"; p.Sizes[2].Hx != want {
		t.Errorf("size 200 hx = %q, want %q", p.Sizes[2].Hx, want)
	}
	if p.Target != "vanished-section" {
		t.Errorf("target = %q", p.Target)
	}
}

func TestSectionPagerParsesLenientlyAndIgnoresUnprefixedParameters(t *testing.T) {
	for _, tc := range []struct {
		query     string
		page, per int
	}{
		{"page=3&per=100", 1, 50},
		{"changed_page=abc&changed_per=75", 1, 50},
		{"changed_page=-1&changed_per=500", 1, 500},
		{"changed_page=99", 3, 50},
	} {
		p := paginateSection(mustURL(t, "/c/1?"+tc.query), "changed", 120)
		if p.Page != tc.page || p.Per != tc.per {
			t.Errorf("?%s: page %d per %d, want %d %d", tc.query, p.Page, p.Per, tc.page, tc.per)
		}
	}
}

func TestOnlySectionsLongerThanTheSmallestPageShowAPager(t *testing.T) {
	for _, tc := range []struct {
		query string
		total int
		shown bool
	}{
		{"", 50, false},
		{"", 51, true},
		{"failures_per=500", 51, true},
	} {
		if got := paginateSection(mustURL(t, "/c/1?"+tc.query), "failures", tc.total).Shown(); got != tc.shown {
			t.Errorf("?%s total %d: shown %v, want %v", tc.query, tc.total, got, tc.shown)
		}
	}
}
