// Package urlnorm is the conservative URL normaliser behind URL discovery:
// one page must not split into many Pages, while different pages stay
// separate. It also holds the sanity limits that keep crawler traps out.
package urlnorm

import (
	"errors"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/idna"
)

// Reasons a URL is dropped (the values stored in dropped_urls.reason).
const (
	TooLong         = "too_long"
	RepeatedSegment = "repeated_segment"
)

// MaxURLLength is the longest URL, in characters, discovery accepts.
const MaxURLLength = 2048

// maxSegmentRepeats is how many times a path segment may appear before the
// URL counts as a crawler trap.
const maxSegmentRepeats = 3

// Normalize returns the canonical form of an absolute http(s) URL: https,
// lowercase host without :443, no fragment or userinfo, dot segments resolved,
// consistent percent-encoding (unreserved characters decoded, hex in
// uppercase), an empty `?` dropped, query parameters sorted and `utm_*`,
// `gclid` and `fbclid` removed. Path case and a trailing slash are kept.
func Normalize(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("urlnorm: not an http(s) URL")
	}
	host, err := normalizeHost(u)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("https://")
	b.WriteString(host)
	path := removeDotSegments(encode(u.EscapedPath(), pathExtra))
	if path == "" {
		path = "/"
	}
	b.WriteString(path)
	if q := normalizeQuery(u.RawQuery); q != "" {
		b.WriteByte('?')
		b.WriteString(q)
	}
	return b.String(), nil
}

// Resolve resolves ref against the absolute URL base (as found in a page, with
// `<base href>` already applied) and normalises the result.
func Resolve(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	return Normalize(b.ResolveReference(r).String())
}

// Host returns the host (with a non-default port) of a normalised URL, or ""
// if it does not parse.
func Host(normalized string) string {
	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	return u.Host
}

// Violation reports which sanity limit a normalised URL breaks (TooLong or
// RepeatedSegment), or "" if it is acceptable.
func Violation(normalized string) string {
	if len(normalized) > MaxURLLength {
		return TooLong
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	seen := map[string]int{}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if seg == "" {
			continue
		}
		seen[seg]++
		if seen[seg] >= maxSegmentRepeats {
			return RepeatedSegment
		}
	}
	return ""
}

func normalizeHost(u *url.URL) (string, error) {
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "" {
		return "", errors.New("urlnorm: no host")
	}
	if !strings.Contains(h, ":") { // not an IPv6 literal
		ascii, err := idna.Lookup.ToASCII(h)
		if err != nil {
			return "", err
		}
		h = ascii
	} else {
		h = "[" + h + "]"
	}
	port := u.Port()
	if port == "443" || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		h += ":" + port
	}
	return h, nil
}

// Characters allowed unescaped beyond the unreserved set.
const (
	pathExtra  = "!$&'()*+,;=:@/"
	queryExtra = "!$'()*+,;=:@/?"
)

func isUnreserved(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

const upperHex = "0123456789ABCDEF"

// encode rewrites s with consistent percent-encoding: unreserved characters
// are literal, characters in extra stay as written, everything else is
// %XX with uppercase hex.
func encode(s, extra string) string {
	var b strings.Builder
	write := func(c byte, wasEscaped bool) {
		switch {
		case isUnreserved(c):
			b.WriteByte(c)
		case !wasEscaped && strings.IndexByte(extra, c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&15])
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				write(hi<<4|lo, true)
				i += 2
				continue
			}
		}
		write(c, false)
	}
	return b.String()
}

// removeDotSegments applies RFC 3986 section 5.2.4 to an absolute path.
func removeDotSegments(p string) string {
	if !strings.Contains(p, ".") {
		return p
	}
	segs := strings.Split(p, "/")
	var out []string
	for i, s := range segs {
		last := i == len(segs)-1
		switch s {
		case ".":
			if last {
				out = append(out, "")
			}
		case "..":
			if len(out) > 1 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, s)
		}
	}
	res := strings.Join(out, "/")
	if !strings.HasPrefix(res, "/") {
		res = "/" + res
	}
	return res
}

func normalizeQuery(raw string) string {
	type pair struct{ key, whole string }
	var pairs []pair
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		key, _, _ := strings.Cut(part, "=")
		key = encode(key, queryExtra)
		if tracking(key) {
			continue
		}
		pairs = append(pairs, pair{key, encode(part, queryExtra)})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.whole
	}
	return strings.Join(parts, "&")
}

// tracking reports whether an encoded query key is a tracking parameter.
func tracking(key string) bool {
	k := strings.ToLower(key)
	return strings.HasPrefix(k, "utm_") || k == "gclid" || k == "fbclid"
}
