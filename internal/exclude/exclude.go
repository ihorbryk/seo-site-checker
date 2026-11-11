// Package exclude matches exclude globs against Page URLs. In a glob `*`
// matches within one path segment, `**` matches across segments, and every
// other character (including `?`) is literal. A glob is matched against the
// whole of the URL's path plus query.
package exclude

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
)

var cache sync.Map // pattern -> *regexp.Regexp

func compile(pattern string) *regexp.Regexp {
	if re, ok := cache.Load(pattern); ok {
		return re.(*regexp.Regexp)
	}
	var b strings.Builder
	b.WriteString(`(?s)^`)
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(`.*`)
			i++
		case pattern[i] == '*':
			b.WriteString(`[^/]*`)
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	b.WriteString(`$`)
	re := regexp.MustCompile(b.String())
	cache.Store(pattern, re)
	return re
}

// Match reports whether target (path plus query) matches the glob pattern.
func Match(pattern, target string) bool {
	if pattern == "" {
		return false
	}
	return compile(pattern).MatchString(target)
}

// Excluded reports whether the URL's path plus query matches any pattern.
// Callers must not exclude the seed Page.
func Excluded(patterns []string, rawURL string) bool {
	if len(patterns) == 0 {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	for _, p := range patterns {
		if Match(p, target) {
			return true
		}
	}
	return false
}
