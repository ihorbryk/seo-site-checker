package urlnorm_test

import (
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/urlnorm"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"already normal", "https://example.com/a", "https://example.com/a"},
		{"http becomes https", "http://example.com/a", "https://example.com/a"},
		{"host lowercased", "https://EXAMPLE.Com/a", "https://example.com/a"},
		{"port 443 dropped", "https://example.com:443/a", "https://example.com/a"},
		{"http port 80 dropped", "http://example.com:80/a", "https://example.com/a"},
		{"other port kept", "https://example.com:8443/a", "https://example.com:8443/a"},
		{"fragment dropped", "https://example.com/a#top", "https://example.com/a"},
		{"empty path is root", "https://example.com", "https://example.com/"},
		{"dot segments resolved", "https://example.com/a/./b/../c", "https://example.com/a/c"},
		{"dot segments above root", "https://example.com/../../a", "https://example.com/a"},
		{"trailing dot segment keeps slash", "https://example.com/a/b/..", "https://example.com/a/"},
		{"unreserved percent-encoding decoded", "https://example.com/%7Euser/%61%2Db", "https://example.com/~user/a-b"},
		{"percent hex uppercased", "https://example.com/a%2fb%c3%a9", "https://example.com/a%2Fb%C3%A9"},
		{"unsafe characters encoded", "https://example.com/a b", "https://example.com/a%20b"},
		{"path case kept", "https://example.com/About/US", "https://example.com/About/US"},
		{"trailing slash kept", "https://example.com/a/", "https://example.com/a/"},
		{"no trailing slash kept", "https://example.com/a", "https://example.com/a"},
		{"empty query dropped", "https://example.com/a?", "https://example.com/a"},
		{"query sorted", "https://example.com/a?b=2&a=1&c=3", "https://example.com/a?a=1&b=2&c=3"},
		{"query sort is stable for repeated keys", "https://example.com/a?k=2&a=1&k=1", "https://example.com/a?a=1&k=2&k=1"},
		{"utm params stripped", "https://example.com/a?utm_source=x&b=1&utm_Medium=y", "https://example.com/a?b=1"},
		{"gclid and fbclid stripped", "https://example.com/a?gclid=1&fbclid=2&q=x", "https://example.com/a?q=x"},
		{"only tracking params leaves no query", "https://example.com/a?utm_source=x", "https://example.com/a"},
		{"query encoding normalised", "https://example.com/a?q=%7e%2f&x=a b", "https://example.com/a?q=~%2F&x=a%20b"},
		{"userinfo dropped", "https://user:pw@example.com/a", "https://example.com/a"},
		{"idna host", "https://BÜCHER.example/a", "https://xn--bcher-kva.example/a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := urlnorm.Normalize(tt.in)
			if err != nil || got != tt.want {
				t.Errorf("Normalize(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestNormalizeRejectsNonWebURLs(t *testing.T) {
	for _, in := range []string{"mailto:a@b.c", "javascript:void(0)", "ftp://example.com/", "https:///a", "", "https://exa mple.com/", "%zz"} {
		if got, err := urlnorm.Normalize(in); err == nil {
			t.Errorf("Normalize(%q) = %q, want an error", in, got)
		}
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name, base, ref, want string
	}{
		{"absolute path", "https://example.com/a/b", "/c", "https://example.com/c"},
		{"relative", "https://example.com/a/b", "c", "https://example.com/a/c"},
		{"relative to a directory", "https://example.com/a/", "c", "https://example.com/a/c"},
		{"parent", "https://example.com/a/b/c", "../d", "https://example.com/a/d"},
		{"scheme relative", "https://example.com/a", "//example.com/x", "https://example.com/x"},
		{"query only", "https://example.com/a?x=1", "?y=2", "https://example.com/a?y=2"},
		{"fragment only is the page itself", "https://example.com/a", "#top", "https://example.com/a"},
		{"absolute", "https://example.com/a", "http://Other.com/x", "https://other.com/x"},
		{"whitespace trimmed", "https://example.com/a", "  /b \n", "https://example.com/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := urlnorm.Resolve(tt.base, tt.ref)
			if err != nil || got != tt.want {
				t.Errorf("Resolve(%q, %q) = %q, %v; want %q", tt.base, tt.ref, got, err, tt.want)
			}
		})
	}
}

func TestHost(t *testing.T) {
	if got := urlnorm.Host("https://shop.test:8443/a"); got != "shop.test:8443" {
		t.Errorf("Host = %q", got)
	}
}

func TestSanityLimit(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))
	tests := []struct {
		name, url, want string
	}{
		{"short", "https://example.com/a/b", ""},
		{"exactly 2048 characters", long, ""},
		{"2049 characters", long + "a", urlnorm.TooLong},
		{"segment twice", "https://example.com/a/a/b", ""},
		{"segment three times", "https://example.com/a/b/a/c/a", urlnorm.RepeatedSegment},
		{"segment three times consecutively", "https://example.com/x/x/x", urlnorm.RepeatedSegment},
		{"different case is a different segment", "https://example.com/a/A/a/A", ""},
		{"empty segments do not count", "https://example.com/a//b//c///", ""},
		{"query is not a segment", "https://example.com/a?/x/x/x/x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := urlnorm.Violation(tt.url); got != tt.want {
				t.Errorf("Violation = %q, want %q", got, tt.want)
			}
		})
	}
}
