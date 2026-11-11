package seo

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Fields maps a SEO field name to every occurrence of its value, in document
// order, whitespace collapsed and case kept. Every key in Keys is present
// (an empty list, never nil) so stored rows and diffs need no special cases.
type Fields map[string][]string

// Keys lists the SEO fields in display order.
var Keys = []string{
	"status", "content_type", "x_robots_tag", "redirect_target",
	"title", "meta_description", "canonical", "meta_robots", "h1",
	"hreflang", "html_lang",
	"og_title", "og_description", "og_image", "og_url",
	"jsonld_types",
}

// Input is what one response gives the extractor.
type Input struct {
	Status  int
	Headers [][2]string // in stored order
	Body    []byte      // HTML responses only
	HasBody bool
}

// Extract reads the SEO fields from a response. The HTML fields stay empty
// when there is no body. redirect_target is filled in by the redirects
// ticket and stays empty until then.
//
// Meta robots values are written "name: content", with the lower-cased meta
// name, so bot-specific tags (googlebot, bingbot, ...) stay apart from the
// generic one. hreflang values are "lang url".
func Extract(in Input) Fields {
	f := Fields{}
	for _, k := range Keys {
		f[k] = []string{}
	}
	f["status"] = []string{strconv.Itoa(in.Status)}
	for _, h := range in.Headers {
		switch strings.ToLower(h[0]) {
		case "content-type":
			f["content_type"] = append(f["content_type"], collapse(h[1]))
		case "x-robots-tag":
			f["x_robots_tag"] = append(f["x_robots_tag"], collapse(h[1]))
		}
	}
	if !in.HasBody {
		return f
	}

	ct := ""
	for _, h := range in.Headers {
		if strings.EqualFold(h[0], "content-type") {
			ct = h[1]
			break
		}
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(Decode(ct, in.Body)))
	if err != nil {
		return f
	}

	add := func(key, v string) { f[key] = append(f[key], v) }
	doc.Find("html").Each(func(_ int, s *goquery.Selection) {
		if v, ok := s.Attr("lang"); ok {
			add("html_lang", collapse(v))
		}
	})
	doc.Find("title").Each(func(_ int, s *goquery.Selection) { add("title", collapse(s.Text())) })
	doc.Find("h1").Each(func(_ int, s *goquery.Selection) { add("h1", collapse(s.Text())) })
	doc.Find("link").Each(func(_ int, s *goquery.Selection) {
		rels := strings.Fields(strings.ToLower(s.AttrOr("rel", "")))
		href := collapse(s.AttrOr("href", ""))
		if hasToken(rels, "canonical") {
			add("canonical", href)
		}
		if hasToken(rels, "alternate") {
			if lang, ok := s.Attr("hreflang"); ok {
				add("hreflang", collapse(lang)+" "+href)
			}
		}
	})
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		content := collapse(s.AttrOr("content", ""))
		if name := strings.ToLower(strings.TrimSpace(s.AttrOr("name", ""))); name != "" {
			switch {
			case name == "description":
				add("meta_description", content)
			case isRobotsName(name):
				add("meta_robots", name+": "+content)
			}
		}
		switch strings.ToLower(strings.TrimSpace(s.AttrOr("property", ""))) {
		case "og:title":
			add("og_title", content)
		case "og:description":
			add("og_description", content)
		case "og:image":
			add("og_image", content)
		case "og:url":
			add("og_url", content)
		}
	})
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		if !strings.EqualFold(strings.TrimSpace(s.AttrOr("type", "")), "application/ld+json") {
			return
		}
		var v any
		if json.Unmarshal([]byte(s.Text()), &v) != nil {
			return
		}
		f["jsonld_types"] = collectTypes(v, f["jsonld_types"])
	})
	return f
}

// collapse trims and folds every run of whitespace into one space.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func hasToken(tokens []string, want string) bool {
	for _, t := range tokens {
		if t == want {
			return true
		}
	}
	return false
}

// isRobotsName reports whether a meta name addresses crawlers: "robots" or a
// bot-specific name such as googlebot, googlebot-news, bingbot or slurp.
func isRobotsName(name string) bool {
	if name == "robots" || name == "slurp" {
		return true
	}
	return strings.HasSuffix(name, "bot") || strings.Contains(name, "bot-")
}

// collectTypes appends every @type found in a JSON-LD value, including those
// of nested objects and @graph members, in a deterministic order.
func collectTypes(v any, out []string) []string {
	switch v := v.(type) {
	case []any:
		for _, e := range v {
			out = collectTypes(e, out)
		}
	case map[string]any:
		switch t := v["@type"].(type) {
		case string:
			out = append(out, collapse(t))
		case []any:
			for _, e := range t {
				if s, ok := e.(string); ok {
					out = append(out, collapse(s))
				}
			}
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			if k != "@type" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = collectTypes(v[k], out)
		}
	}
	return out
}
