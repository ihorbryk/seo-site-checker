package links_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/links"
)

func render(ls []links.Link) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Source+" "+l.URL)
	}
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{"anchor", `<a href="/a">x</a>`, []string{"a https://shop.test/a"}},
		{"relative anchor", `<a href="b">x</a>`, []string{"a https://shop.test/dir/b"}},
		{"canonical", `<link rel="canonical" href="https://shop.test/c">`, []string{"canonical https://shop.test/c"}},
		{"hreflang alternate", `<link rel="alternate" hreflang="de" href="/de/">`, []string{"hreflang https://shop.test/de/"}},
		{"alternate without hreflang ignored", `<link rel="alternate" type="application/rss+xml" href="/feed">`, nil},
		{"rel tokens", `<link rel="Alternate Home" hreflang="fr" href="/fr">`, []string{"hreflang https://shop.test/fr"}},
		{"nofollow ignored", `<a rel="nofollow" href="/n">x</a><a rel="noopener NoFollow" href="/m">y</a>`, nil},
		{"other rel kept", `<a rel="noopener" href="/o">x</a>`, []string{"a https://shop.test/o"}},
		{"base href honoured", `<base href="/sub/"><a href="p">x</a>`, []string{"a https://shop.test/sub/p"}},
		{"absolute base href", `<base href="https://shop.test/z/"><a href="p">x</a>`, []string{"a https://shop.test/z/p"}},
		{"off-host ignored", `<a href="https://other.test/x">x</a>`, nil},
		{"subdomain ignored", `<a href="https://www.shop.test/x">x</a>`, nil},
		{"host case and http normalised", `<a href="HTTP://SHOP.test:443/x#f">x</a>`, []string{"a https://shop.test/x"}},
		{"non-web schemes ignored", `<a href="mailto:a@b.c">x</a><a href="javascript:void(0)">y</a><a href="tel:1">z</a>`, nil},
		{"self link kept", `<a href="/dir/">x</a>`, []string{"a https://shop.test/dir/"}},
		{"fragment-only link is a self link", `<a href="#top">x</a>`, []string{"a https://shop.test/dir/"}},
		{"duplicates collapse per source", `<a href="/a">1</a><a href="/a/../a">2</a><link rel="canonical" href="/a">`,
			[]string{"a https://shop.test/a", "canonical https://shop.test/a"}},
		{"slash variants stay separate", `<a href="/a">1</a><a href="/a/">2</a>`,
			[]string{"a https://shop.test/a", "a https://shop.test/a/"}},
		{"empty href is a self link, anchor without href ignored", `<a href="">x</a><a>y</a>`, []string{"a https://shop.test/dir/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := links.Extract([]byte("<html><head></head><body>"+tt.html+"</body></html>"), "https://shop.test/dir/", "shop.test")
			if err != nil {
				t.Fatal(err)
			}
			if got := render(res.Links); !eq(got, tt.want) {
				t.Errorf("links = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractDropsCrawlerTrapsOnTheProjectHost(t *testing.T) {
	long := "/" + strings.Repeat("a", 2100)
	html := `<a href="` + long + `">x</a><a href="/x/y/x/z/x">y</a><a href="https://other.test` + long + `">z</a><a href="/ok">ok</a>`
	res, err := links.Extract([]byte(html), "https://shop.test/", "shop.test")
	if err != nil {
		t.Fatal(err)
	}
	if got := render(res.Links); !eq(got, []string{"a https://shop.test/ok"}) {
		t.Errorf("links = %q", got)
	}
	reasons := map[string]string{}
	for _, d := range res.Dropped {
		reasons[d.URL] = d.Reason
	}
	if len(res.Dropped) != 2 || reasons["https://shop.test"+long] != "too_long" || reasons["https://shop.test/x/y/x/z/x"] != "repeated_segment" {
		t.Errorf("dropped = %+v", res.Dropped)
	}
}
