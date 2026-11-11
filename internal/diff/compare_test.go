package diff

import (
	"reflect"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/seo"
)

func TestCompareHeaders(t *testing.T) {
	h := func(kv ...string) [][2]string {
		var out [][2]string
		for i := 0; i < len(kv); i += 2 {
			out = append(out, [2]string{kv[i], kv[i+1]})
		}
		return out
	}
	tests := []struct {
		name          string
		before, after [][2]string
		ignored       []string
		want          []HeaderChange
	}{
		{"identical", h("A", "1"), h("A", "1"), nil, nil},
		{"reordered", h("A", "1", "B", "2"), h("B", "2", "A", "1"), nil, nil},
		{"name case is ignored", h("Content-Type", "x"), h("content-type", "x"), nil, nil},
		{"value case matters", h("A", "x"), h("A", "X"), nil,
			[]HeaderChange{{Name: "a", Kind: HeaderChanged, Before: []string{"x"}, After: []string{"X"}}}},
		{"added", h("A", "1"), h("A", "1", "B", "2"), nil,
			[]HeaderChange{{Name: "b", Kind: HeaderAdded, After: []string{"2"}}}},
		{"removed", h("A", "1", "B", "2"), h("A", "1"), nil,
			[]HeaderChange{{Name: "b", Kind: HeaderRemoved, Before: []string{"2"}}}},
		{"repeated header is a multiset", h("Link", "a", "Link", "b"), h("Link", "b", "Link", "a"), nil, nil},
		{"duplicate count matters", h("Link", "a"), h("Link", "a", "Link", "a"), nil,
			[]HeaderChange{{Name: "link", Kind: HeaderChanged, Before: []string{"a"}, After: []string{"a", "a"}}}},
		{"ignored names are removed from both sides", h("Date", "1", "A", "1"), h("Date", "2", "A", "1"), []string{"date"}, nil},
		{"ignored names match case-insensitively", h("ETag", "1"), nil, []string{"etag"}, nil},
		{"sorted by name", h(), h("Z", "1", "B", "1"), nil, []HeaderChange{
			{Name: "b", Kind: HeaderAdded, After: []string{"1"}},
			{Name: "z", Kind: HeaderAdded, After: []string{"1"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareHeaders(tt.before, tt.after, tt.ignored)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCompareSEO(t *testing.T) {
	f := func(kv ...any) seo.Fields {
		out := seo.Fields{}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1].([]string)
		}
		return out
	}
	tests := []struct {
		name          string
		before, after seo.Fields
		want          map[string]SEOChange
	}{
		{"equal", f("title", []string{"A"}), f("title", []string{"A"}), nil},
		{"missing key equals empty list", f(), f("h1", []string{}), nil},
		{"title changed", f("title", []string{"A"}), f("title", []string{"B"}),
			map[string]SEOChange{"title": {Before: []string{"A"}, After: []string{"B"}}}},
		{"ordered list: order matters", f("h1", []string{"a", "b"}), f("h1", []string{"b", "a"}),
			map[string]SEOChange{"h1": {Before: []string{"a", "b"}, After: []string{"b", "a"}}}},
		{"ordered list: duplicates matter", f("h1", []string{"a"}), f("h1", []string{"a", "a"}),
			map[string]SEOChange{"h1": {Before: []string{"a"}, After: []string{"a", "a"}}}},
		{"hreflang is an unordered set", f("hreflang", []string{"en /a", "de /b"}), f("hreflang", []string{"de /b", "en /a"}), nil},
		{"jsonld types are an unordered set", f("jsonld_types", []string{"Product", "Offer"}), f("jsonld_types", []string{"Offer", "Product"}), nil},
		{"unordered set still detects a change", f("hreflang", []string{"en /a"}), f("hreflang", []string{"en /a", "de /b"}),
			map[string]SEOChange{"hreflang": {Before: []string{"en /a"}, After: []string{"en /a", "de /b"}}}},
		{"a field only in after", f(), f("canonical", []string{"/x"}),
			map[string]SEOChange{"canonical": {Before: []string{}, After: []string{"/x"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareSEO(tt.before, tt.after)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func change(before, after string) SEOChange {
	return SEOChange{Before: []string{before}, After: []string{after}}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		changes map[string]SEOChange
		headers bool
		body    bool
		want    Tier
	}{
		{"nothing", nil, false, false, TierEmpty},
		{"body only", nil, false, true, TierBody},
		{"headers only", nil, true, false, TierHeaders},
		{"headers and body", nil, true, true, TierHeaders},
		{"title", map[string]SEOChange{"title": change("a", "b")}, true, true, TierSEO},
		{"h1", map[string]SEOChange{"h1": change("a", "b")}, false, false, TierSEO},
		{"og tag", map[string]SEOChange{"og_title": change("a", "b")}, false, false, TierSEO},
		{"hreflang", map[string]SEOChange{"hreflang": change("a", "b")}, false, false, TierSEO},
		{"status within a class", map[string]SEOChange{"status": change("200", "204")}, false, false, TierSEO},
		{"status class 2xx to 4xx", map[string]SEOChange{"status": change("200", "404")}, false, false, TierCritical},
		{"status class 2xx to 3xx", map[string]SEOChange{"status": change("200", "301")}, false, false, TierCritical},
		{"status 404 to 410 is the same class", map[string]SEOChange{"status": change("404", "410")}, false, false, TierSEO},
		{"meta robots", map[string]SEOChange{"meta_robots": change("robots: index", "robots: noindex")}, false, false, TierCritical},
		{"x robots tag", map[string]SEOChange{"x_robots_tag": change("", "noindex")}, true, false, TierCritical},
		{"canonical", map[string]SEOChange{"canonical": change("/a", "/b")}, false, false, TierCritical},
		{"redirect target", map[string]SEOChange{"redirect_target": change("/a", "/b")}, false, false, TierCritical},
		{"critical wins over seo", map[string]SEOChange{"title": change("a", "b"), "canonical": change("a", "b")}, true, true, TierCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.changes, tt.headers, tt.body); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
