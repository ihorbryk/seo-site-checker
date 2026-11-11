package robots_test

import (
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/robots"
)

func TestProductToken(t *testing.T) {
	for ua, want := range map[string]string{
		"SEOSiteChecker/0.1":                              "SEOSiteChecker",
		"Googlebot/2.1 (+http://www.google.com/bot.html)": "Googlebot",
		"my_bot-x":  "my_bot-x",
		"  Bingbot": "Bingbot",
		"":          "",
		"/weird":    "",
		"Mozilla/5.0 (compatible; Googlebot/2.1)": "Mozilla",
	} {
		if got := robots.ProductToken(ua); got != want {
			t.Errorf("ProductToken(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestAllowed(t *testing.T) {
	tests := []struct {
		name string
		body string
		ua   string
		path string
		want bool
	}{
		{"empty file allows", "", "Bot", "/a", true},
		{"disallow prefix", "User-agent: *\nDisallow: /private", "Bot", "/private/x", false},
		{"disallow prefix other path", "User-agent: *\nDisallow: /private", "Bot", "/public", true},
		{"empty disallow allows all", "User-agent: *\nDisallow:", "Bot", "/a", true},
		{"disallow root", "User-agent: *\nDisallow: /", "Bot", "/", false},
		{"field names are case-insensitive", "USER-AGENT: *\nDISALLOW: /x", "Bot", "/x", false},
		{"comments and blank lines", "# hi\nUser-agent: * # all\n\nDisallow: /x # no\n", "Bot", "/x", false},
		{"BOM and CRLF", "\xef\xbb\xbfUser-agent: *\r\nDisallow: /x\r\n", "Bot", "/x", false},
		{"longer allow beats disallow", "User-agent: *\nDisallow: /a\nAllow: /a/b", "Bot", "/a/b/c", true},
		{"longer disallow beats allow", "User-agent: *\nAllow: /a\nDisallow: /a/b", "Bot", "/a/b/c", false},
		{"allow wins a tie", "User-agent: *\nDisallow: /a\nAllow: /a", "Bot", "/a", true},
		{"allow wins a tie regardless of order", "User-agent: *\nAllow: /a\nDisallow: /a", "Bot", "/a", true},
		{"star wildcard", "User-agent: *\nDisallow: /*.pdf", "Bot", "/files/x.pdf", false},
		{"star wildcard no match", "User-agent: *\nDisallow: /*.pdf", "Bot", "/files/x.html", true},
		{"star matches across slashes and empty", "User-agent: *\nDisallow: /a*b", "Bot", "/ab", false},
		{"dollar anchors the end", "User-agent: *\nDisallow: /*.pdf$", "Bot", "/x.pdf", false},
		{"dollar does not match a longer path", "User-agent: *\nDisallow: /*.pdf$", "Bot", "/x.pdf?download=1", true},
		{"dollar exact path", "User-agent: *\nDisallow: /exact$", "Bot", "/exact/more", true},
		{"query is part of the path", "User-agent: *\nDisallow: /search?q=", "Bot", "/search?q=shoes", false},
		{"rules are case-sensitive", "User-agent: *\nDisallow: /Private", "Bot", "/private", true},
		{"specific group wins over star", "User-agent: *\nDisallow: /\n\nUser-agent: Googlebot\nDisallow: /g", "Googlebot", "/a", true},
		{"specific group applies", "User-agent: *\nDisallow: /\n\nUser-agent: Googlebot\nDisallow: /g", "Googlebot", "/g/x", false},
		{"agent match is case-insensitive", "User-agent: googlebot\nDisallow: /g", "GoogleBot", "/g", false},
		{"unmatched agent falls back to star", "User-agent: Googlebot\nDisallow: /g\n\nUser-agent: *\nDisallow: /s", "Other", "/s", false},
		{"unmatched agent without star allows", "User-agent: Googlebot\nDisallow: /g", "Other", "/g", true},
		{"consecutive user-agent lines share rules", "User-agent: A\nUser-agent: B\nDisallow: /x", "B", "/x", false},
		{"groups of the same agent merge", "User-agent: A\nDisallow: /x\n\nUser-agent: A\nDisallow: /y", "A", "/y", false},
		{"unknown fields are ignored", "User-agent: *\nCrawl-delay: 10\nSitemap: https://x/s.xml\nDisallow: /x", "Bot", "/x", false},
		{"rules before any user-agent are ignored", "Disallow: /x\nUser-agent: *\nDisallow: /y", "Bot", "/x", true},
		{"empty allow is ignored", "User-agent: *\nAllow:\nDisallow: /x", "Bot", "/x", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := robots.Parse([]byte(tc.body)).Allowed(tc.ua, tc.path)
			if got != tc.want {
				t.Errorf("Allowed(%q, %q) = %v, want %v\n%s", tc.ua, tc.path, got, tc.want, tc.body)
			}
		})
	}
}
