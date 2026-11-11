// Package diff holds the pure logic behind Diffs: the HTML reformatter, Noise
// rules, header multiset compare, SEO field compare and tier classification.
// The worker uses it to precompute Diff summaries; the Diff page uses the same
// code to compute the full diff on view.
package diff

import (
	"strings"

	"golang.org/x/net/html"
)

// Reformat rewrites HTML as one tag, comment or text node per line, with
// whitespace runs folded to one space and blank text dropped, so minified and
// pretty-printed versions of a page diff the same.
func Reformat(src string) string {
	var out strings.Builder
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		line := strings.Join(strings.Fields(string(z.Raw())), " ")
		if line == "" {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}
