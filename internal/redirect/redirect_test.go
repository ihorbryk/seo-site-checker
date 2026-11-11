package redirect_test

import (
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/redirect"
)

func TestClassify(t *testing.T) {
	const page = "https://shop.test/old"
	loc := func(v string) [][2]string { return [][2]string{{"Content-Type", "text/html"}, {"Location", v}} }
	tests := []struct {
		name    string
		status  int
		headers [][2]string
		want    redirect.Target
	}{
		{"301 relative", 301, loc("/new"), redirect.Target{URL: "https://shop.test/new", Kind: redirect.OnDomain}},
		{"302 absolute", 302, loc("https://shop.test/a"), redirect.Target{URL: "https://shop.test/a", Kind: redirect.OnDomain}},
		{"303", 303, loc("/x"), redirect.Target{URL: "https://shop.test/x", Kind: redirect.OnDomain}},
		{"307", 307, loc("/x"), redirect.Target{URL: "https://shop.test/x", Kind: redirect.OnDomain}},
		{"308", 308, loc("/x"), redirect.Target{URL: "https://shop.test/x", Kind: redirect.OnDomain}},
		{"normalised", 301, loc("https://SHOP.test:443/a/../b?utm_source=x&z=1&a=2#frag"), redirect.Target{URL: "https://shop.test/b?a=2&z=1", Kind: redirect.OnDomain}},
		{"header name case", 301, [][2]string{{"location", "/x"}}, redirect.Target{URL: "https://shop.test/x", Kind: redirect.OnDomain}},
		{"self", 301, loc("/old"), redirect.Target{URL: "https://shop.test/old", Kind: redirect.OnDomain}},
		{"off-domain", 301, loc("https://other.test/"), redirect.Target{URL: "https://other.test/", Kind: redirect.OffDomain}},
		{"subdomain is off-domain", 301, loc("https://www.shop.test/"), redirect.Target{URL: "https://www.shop.test/", Kind: redirect.OffDomain}},
		{"http on-domain is recorded only", 301, loc("http://shop.test/a"), redirect.Target{URL: "http://shop.test/a", Kind: redirect.Insecure}},
		{"http off-domain", 301, loc("http://other.test/a"), redirect.Target{URL: "http://other.test/a", Kind: redirect.Insecure}},
		{"300 has no target", 300, loc("/x"), redirect.Target{}},
		{"304 has no target", 304, loc("/x"), redirect.Target{}},
		{"200 has no target", 200, loc("/x"), redirect.Target{}},
		{"missing Location", 301, nil, redirect.Target{}},
		{"empty Location", 301, loc("  "), redirect.Target{}},
		{"unparseable Location", 301, loc("http://[::1"), redirect.Target{}},
		{"non-http scheme", 301, loc("mailto:a@b.test"), redirect.Target{}},
		{"first Location wins", 301, [][2]string{{"Location", "/a"}, {"Location", "/b"}}, redirect.Target{URL: "https://shop.test/a", Kind: redirect.OnDomain}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redirect.Classify(tt.status, tt.headers, page, "shop.test"); got != tt.want {
				t.Errorf("Classify = %+v, want %+v", got, tt.want)
			}
		})
	}
}
