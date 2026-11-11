package seo

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestExtractGolden(t *testing.T) {
	htmlCT := [][2]string{{"Content-Type", "text/html; charset=utf-8"}}
	tests := []struct {
		name    string
		page    string // file in testdata/, empty for no body
		status  int
		headers [][2]string
	}{
		{"shop_product", "shop_product.html", 200, append([][2]string{{"X-Robots-Tag", "noarchive"}, {"X-Robots-Tag", "googlebot: nosnippet"}}, htmlCT...)},
		{"blog_post_legacy_charset", "blog_post_latin1.html", 200, [][2]string{{"Content-Type", "text/html"}}},
		{"messy_markup", "messy.html", 404, htmlCT},
		{"multilingual_home", "multilingual.html", 200, htmlCT},
		{"empty_head", "empty.html", 200, htmlCT},
		{"non_html_pdf", "", 200, [][2]string{{"Content-Type", "application/pdf"}, {"X-Robots-Tag", "noindex"}}},
		{"redirect", "", 301, [][2]string{{"Location", "/new"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{Status: tt.status, Headers: tt.headers}
			if tt.page != "" {
				body, err := os.ReadFile(filepath.Join("testdata", tt.page))
				if err != nil {
					t.Fatal(err)
				}
				in.Body, in.HasBody = body, true
			}
			got, err := json.MarshalIndent(Extract(in), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			golden := filepath.Join("testdata", tt.name+".golden.json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s differs (run with -update to accept):\n%s", golden, got)
			}
		})
	}
}

func TestExtractNonHTMLHasEmptyListsNotNull(t *testing.T) {
	f := Extract(Input{Status: 200, Headers: [][2]string{{"Content-Type", "application/json"}}})
	for _, k := range Keys {
		if f[k] == nil {
			t.Errorf("%s is nil; want an empty list", k)
		}
	}
	if len(f["title"]) != 0 || len(f["status"]) != 1 {
		t.Errorf("unexpected fields: %v", f)
	}
}
