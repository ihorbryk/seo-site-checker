package diff

import (
	"slices"
	"sort"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/seo"
)

// HeaderChangeKind says how one header name differs between two Snapshots.
type HeaderChangeKind string

const (
	HeaderChanged HeaderChangeKind = "changed"
	HeaderAdded   HeaderChangeKind = "added"
	HeaderRemoved HeaderChangeKind = "removed"
)

// HeaderChange is one lower-cased header name whose values differ. Before and
// After hold the name's values, sorted.
type HeaderChange struct {
	Name   string
	Kind   HeaderChangeKind
	Before []string
	After  []string
}

// CompareHeaders compares two header lists as multisets of (lower-cased name,
// value), ignoring order and the lower-cased names in ignored. It returns the
// names that differ, sorted by name.
func CompareHeaders(before, after [][2]string, ignored []string) []HeaderChange {
	b, a := groupHeaders(before, ignored), groupHeaders(after, ignored)
	names := map[string]bool{}
	for n := range b {
		names[n] = true
	}
	for n := range a {
		names[n] = true
	}
	var out []HeaderChange
	for n := range names {
		bv, av := b[n], a[n]
		if slices.Equal(bv, av) {
			continue
		}
		c := HeaderChange{Name: n, Kind: HeaderChanged, Before: bv, After: av}
		switch {
		case len(bv) == 0:
			c.Kind = HeaderAdded
		case len(av) == 0:
			c.Kind = HeaderRemoved
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func groupHeaders(hs [][2]string, ignored []string) map[string][]string {
	m := map[string][]string{}
	for _, h := range hs {
		n := strings.ToLower(h[0])
		if slices.Contains(ignored, n) {
			continue
		}
		m[n] = append(m[n], h[1])
	}
	for _, vs := range m {
		sort.Strings(vs)
	}
	return m
}

// SEOChange is one SEO field's values before and after, as stored.
type SEOChange struct {
	Before []string `json:"before"`
	After  []string `json:"after"`
}

// unorderedFields are compared as sets; every other SEO field is an ordered list.
var unorderedFields = map[string]bool{"hreflang": true, "jsonld_types": true}

// CompareSEO returns the SEO fields that differ. A field missing from one side
// counts as an empty list. Noise rules never apply here.
func CompareSEO(before, after seo.Fields) map[string]SEOChange {
	out := map[string]SEOChange{}
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	for k := range keys {
		b, a := before[k], after[k]
		if unorderedFields[k] {
			if slices.Equal(uniqueSorted(b), uniqueSorted(a)) {
				continue
			}
		} else if slices.Equal(b, a) {
			continue
		}
		out[k] = SEOChange{Before: nonNil(b), After: nonNil(a)}
	}
	return out
}

func uniqueSorted(vs []string) []string {
	out := slices.Clone(vs)
	sort.Strings(out)
	return slices.Compact(out)
}

func nonNil(vs []string) []string {
	if vs == nil {
		return []string{}
	}
	return vs
}
