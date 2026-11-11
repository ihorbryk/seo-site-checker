// Package robots parses robots.txt and answers whether a user agent may fetch
// a path, following RFC 9309: groups are chosen by the first token of the user
// agent (case-insensitive, `*` as fallback), the longest matching rule wins,
// Allow wins ties, and `*` and `$` work in patterns.
package robots

import "strings"

type rule struct {
	allow   bool
	pattern string
}

type group struct {
	agents []string // lowercased
	rules  []rule
}

// Robots is a parsed robots.txt.
type Robots struct {
	groups []*group
}

// ProductToken is the first token of a user agent: its leading run of letters,
// `_` and `-` (so "Googlebot/2.1 (+http://...)" is "Googlebot").
func ProductToken(userAgent string) string {
	ua := strings.TrimSpace(userAgent)
	end := 0
	for end < len(ua) {
		c := ua[end]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-' {
			end++
			continue
		}
		break
	}
	return ua[:end]
}

// Parse reads a robots.txt body. It never fails: lines it does not understand
// are ignored.
func Parse(body []byte) *Robots {
	text := strings.TrimPrefix(string(body), "\xef\xbb\xbf")
	r := &Robots{}
	var cur *group
	inAgents := false // the previous line was a user-agent line
	for _, line := range strings.Split(text, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		field = strings.ToLower(strings.TrimSpace(field))
		value = strings.TrimSpace(value)
		switch field {
		case "user-agent":
			if !inAgents || cur == nil {
				cur = &group{}
				r.groups = append(r.groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(value))
			inAgents = true
		case "allow", "disallow":
			inAgents = false
			if cur == nil || value == "" {
				continue
			}
			cur.rules = append(cur.rules, rule{allow: field == "allow", pattern: value})
		default:
			inAgents = false
		}
	}
	return r
}

// Allowed reports whether userAgent may fetch path, which is the URL's path
// plus its query string ("/a?b=c").
func (r *Robots) Allowed(userAgent, path string) bool {
	rules := r.rulesFor(strings.ToLower(ProductToken(userAgent)))
	var best *rule
	for i := range rules {
		ru := &rules[i]
		if !matches(ru.pattern, path) {
			continue
		}
		switch {
		case best == nil, len(ru.pattern) > len(best.pattern),
			len(ru.pattern) == len(best.pattern) && ru.allow:
			best = ru
		}
	}
	return best == nil || best.allow
}

// rulesFor merges the groups naming the product token, or else those naming `*`.
func (r *Robots) rulesFor(token string) []rule {
	var rules []rule
	for _, want := range []string{token, "*"} {
		if want == "" {
			continue
		}
		found := false
		for _, g := range r.groups {
			for _, a := range g.agents {
				if a == want {
					rules = append(rules, g.rules...)
					found = true
					break
				}
			}
		}
		if found {
			return rules
		}
	}
	return nil
}

// matches reports whether path starts with pattern, where `*` matches any run
// of characters and a trailing `$` anchors the end.
func matches(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	pos := len(parts[0])
	if len(parts) == 1 {
		return !anchored || pos == len(path)
	}
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(path[pos:], p)
		if i < 0 {
			return false
		}
		pos += i + len(p)
	}
	last := parts[len(parts)-1]
	if !anchored {
		return strings.Contains(path[pos:], last)
	}
	return len(path)-pos >= len(last) && strings.HasSuffix(path, last)
}
