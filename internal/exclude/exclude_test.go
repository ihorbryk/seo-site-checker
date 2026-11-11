package exclude_test

import (
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/exclude"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, target string
		want            bool
	}{
		{"/blog/*", "/blog/post", true},
		{"/blog/*", "/blog/", true},
		{"/blog/*", "/blog/a/b", false},
		{"/blog/**", "/blog/a/b", true},
		{"/blog/**", "/blog/", true},
		{"/blog/**", "/blog", false},
		{"/**/draft", "/a/b/draft", true},
		{"/**/draft", "/a/draft", true},
		{"/**/draft", "/draft", false},
		{"/a*c", "/abc", true},
		{"/a*c", "/ac", true},
		{"/a*c", "/a/c", false},
		{"/search?q=*", "/search?q=shoes", true},
		{"/search?q=*", "/search?q=a/b", false},
		{"/search?q=**", "/search?q=a/b", true},
		{"/search?*", "/search?a=1&b=2", true},
		{"/search?*", "/searchX", false}, // ? is literal
		{"/a?b", "/ab", false},
		{"/a?b", "/a?b", true},
		{"/a.html", "/aXhtml", false},      // . is literal
		{"/a+b(c)[d]", "/a+b(c)[d]", true}, // regexp metacharacters are literal
		{"/exact", "/exact", true},
		{"/exact", "/exact/", false},
		{"/exact", "/exact?x=1", false},
		{"/Exact", "/exact", false},
		{"", "/", false},
		{"**", "/anything/at?all=1", true},
		{"/", "/", true},
	}
	for _, tt := range tests {
		if got := exclude.Match(tt.pattern, tt.target); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.target, got, tt.want)
		}
	}
}

func TestExcluded(t *testing.T) {
	tests := []struct {
		patterns []string
		url      string
		want     bool
	}{
		{[]string{"/private/**"}, "https://shop.test/private/a/b", true},
		{[]string{"/private/**"}, "https://shop.test/public", false},
		{[]string{"/x", "/search?q=*"}, "https://shop.test/search?q=hat", true},
		{[]string{"/search"}, "https://shop.test/search?q=hat", false}, // query is part of the target
		{[]string{"/a"}, "https://shop.test/a#frag", true},
		{nil, "https://shop.test/a", false},
		{[]string{"**"}, "https://shop.test/", true},
	}
	for _, tt := range tests {
		if got := exclude.Excluded(tt.patterns, tt.url); got != tt.want {
			t.Errorf("Excluded(%v, %q) = %v, want %v", tt.patterns, tt.url, got, tt.want)
		}
	}
}
