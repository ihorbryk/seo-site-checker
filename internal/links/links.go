// Package links extracts the on-domain links of an HTML page that URL
// discovery follows: <a href>, canonical and hreflang alternates, with
// <base href> honoured and nofollow ignored.
package links

import (
	"bytes"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/urlnorm"
)

// Link sources as stored in links.source.
const (
	SourceAnchor    = "a"
	SourceCanonical = "canonical"
	SourceHreflang  = "hreflang"
	SourceRedirect  = "redirect"
)

// Link is one normalised URL on the Project's host and where it was found.
type Link struct {
	URL    string
	Source string
}

// Dropped is an on-host URL refused by a sanity limit.
type Dropped struct {
	URL    string
	Reason string
}

// Result is what a page's HTML links to.
type Result struct {
	Links   []Link
	Dropped []Dropped
}

// Extract returns the exact-host links of html, the body of the page at
// pageURL (normalised). host is the Project's domain. Links are distinct per
// (URL, source); self-links are included.
func Extract(html []byte, pageURL, host string) (Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return Result{}, err
	}
	base := pageURL
	if href, ok := doc.Find("base[href]").First().Attr("href"); ok {
		if b, err := urlnorm.Resolve(pageURL, href); err == nil {
			base = b
		}
	}

	var res Result
	seen := map[Link]bool{}
	droppedSeen := map[string]bool{}
	add := func(href, source string) {
		u, err := urlnorm.Resolve(base, href)
		if err != nil || urlnorm.Host(u) != host {
			return
		}
		if reason := urlnorm.Violation(u); reason != "" {
			if !droppedSeen[u] {
				droppedSeen[u] = true
				res.Dropped = append(res.Dropped, Dropped{URL: u, Reason: reason})
			}
			return
		}
		l := Link{URL: u, Source: source}
		if !seen[l] {
			seen[l] = true
			res.Links = append(res.Links, l)
		}
	}

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		if hasToken(s.AttrOr("rel", ""), "nofollow") {
			return
		}
		add(s.AttrOr("href", ""), SourceAnchor)
	})
	doc.Find("link[href]").Each(func(_ int, s *goquery.Selection) {
		rel := s.AttrOr("rel", "")
		switch {
		case hasToken(rel, "canonical"):
			add(s.AttrOr("href", ""), SourceCanonical)
		case hasToken(rel, "alternate") && strings.TrimSpace(s.AttrOr("hreflang", "")) != "":
			add(s.AttrOr("href", ""), SourceHreflang)
		}
	})
	return res, nil
}

func hasToken(attr, token string) bool {
	for _, f := range strings.Fields(strings.ToLower(attr)) {
		if f == token {
			return true
		}
	}
	return false
}
