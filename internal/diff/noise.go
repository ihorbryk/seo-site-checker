package diff

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Rule kinds as stored on a Check.
const (
	KindHeader   = "header"
	KindBodyMask = "body_mask"
)

// Masked replaces every body mask match, on both sides of a Diff.
const Masked = "⟨masked⟩"

// Rule is one Noise rule: a header name to ignore, or an RE2 pattern whose
// matches are masked in the reformatted HTML.
type Rule struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Rules is a set of Noise rules, as frozen onto a Check.
type Rules []Rule

// BuiltinRules returns the fixed built-in Noise rules.
func BuiltinRules() Rules {
	var rs Rules
	for _, h := range []string{
		"Date", "Age", "Expires", "Set-Cookie", "ETag", "Last-Modified",
		"CF-Ray", "X-Request-Id", "Server-Timing", "Report-To", "NEL",
	} {
		rs = append(rs, Rule{Kind: KindHeader, Value: h})
	}
	for _, m := range []string{
		// nonce attributes
		`(?i)\bnonce\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>"']+)`,
		// CSRF-looking hidden inputs, either attribute order
		`(?i)<input\b[^>]*\btype\s*=\s*["']?hidden\b[^>]*\bname\s*=\s*["']?[^"'>\s]*(?:csrf|xsrf|authenticity_token)[^>]*>`,
		`(?i)<input\b[^>]*\bname\s*=\s*["']?[^"'>\s]*(?:csrf|xsrf|authenticity_token)[^"'>\s]*["']?[^>]*\btype\s*=\s*["']?hidden\b[^>]*>`,
		// CSRF-looking meta tags
		`(?i)<meta\b[^>]*\bname\s*=\s*["']?[^"'>\s]*(?:csrf|xsrf)[^>]*>`,
	} {
		rs = append(rs, Rule{Kind: KindBodyMask, Value: m})
	}
	return rs
}

// ParseRules decodes a Check's frozen Noise rules.
func ParseRules(raw []byte) (Rules, error) {
	var rs Rules
	err := json.Unmarshal(raw, &rs)
	return rs, err
}

// JSON encodes the rules for storing on a Check.
func (rs Rules) JSON() ([]byte, error) { return json.Marshal(rs) }

// IgnoredHeaders returns the lower-cased header names to ignore.
func (rs Rules) IgnoredHeaders() []string {
	var out []string
	for _, r := range rs {
		if r.Kind == KindHeader {
			out = append(out, strings.ToLower(strings.TrimSpace(r.Value)))
		}
	}
	return out
}

// Masks compiles the body masks. A pattern that does not compile is skipped:
// patterns are validated when saved, so this only guards stored data.
func (rs Rules) Masks() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, r := range rs {
		if r.Kind != KindBodyMask {
			continue
		}
		if re, err := regexp.Compile(r.Value); err == nil {
			out = append(out, re)
		}
	}
	return out
}

// ApplyMasks replaces each match of each mask, in order, with Masked.
func ApplyMasks(s string, masks []*regexp.Regexp) string {
	for _, m := range masks {
		s = m.ReplaceAllString(s, Masked)
	}
	return s
}
