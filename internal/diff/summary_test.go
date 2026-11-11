package diff

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/seo"
)

func side(body string, headers ...[2]string) Side {
	h := sha256.Sum256([]byte(body))
	return Side{
		Headers:    headers,
		SEO:        seo.Fields{"status": {"200"}},
		BodySHA256: h[:],
		LoadBody:   func() ([]byte, error) { return []byte(body), nil },
	}
}

func TestSummarizeBody(t *testing.T) {
	rules := BuiltinRules()
	tests := []struct {
		name          string
		before, after Side
		want          Tier
	}{
		{"same body", side("<p>a</p>"), side("<p>a</p>"), TierEmpty},
		{"different body", side("<p>a</p>"), side("<p>b</p>"), TierBody},
		{"whitespace-only change is empty", side("<p>a</p><p>b</p>"), side("<p>a</p>\n  <p>b</p>"), TierEmpty},
		{"nonce-only change is empty", side(`<script nonce="1">x</script>`), side(`<script nonce="2">x</script>`), TierEmpty},
		{"csrf-only change is empty",
			side(`<form><input type="hidden" name="csrf" value="1"></form>`),
			side(`<form><input type="hidden" name="csrf" value="2"></form>`), TierEmpty},
		{"change next to a nonce still counts", side(`<script nonce="1">x</script>`), side(`<script nonce="2">y</script>`), TierBody},
		{"body added", Side{SEO: seo.Fields{"status": {"200"}}}, side("<p>a</p>"), TierBody},
		{"body removed", side("<p>a</p>"), Side{SEO: seo.Fields{"status": {"200"}}}, TierBody},
		{"no body on either side", Side{}, Side{}, TierEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Summarize(tt.before, tt.after, rules)
			if err != nil {
				t.Fatal(err)
			}
			if got.Tier != tt.want {
				t.Errorf("tier %s, want %s (%+v)", got.Tier, tt.want, got)
			}
		})
	}
}

func TestSummarizeEqualHashesSkipTheBodyLoad(t *testing.T) {
	a := side("<p>a</p>")
	a.LoadBody = func() ([]byte, error) { return nil, errors.New("must not load") }
	b := side("<p>a</p>")
	if _, err := Summarize(a, b, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSummarizeFlags(t *testing.T) {
	before := side("<p>a</p>", [2]string{"Date", "1"}, [2]string{"X-Powered-By", "a"})
	before.SEO = seo.Fields{"status": {"200"}, "title": {"A"}}
	after := side("<p>a</p>", [2]string{"Date", "2"}, [2]string{"X-Powered-By", "b"})
	after.SEO = seo.Fields{"status": {"404"}, "title": {"A"}}
	got, err := Summarize(before, after, BuiltinRules())
	if err != nil {
		t.Fatal(err)
	}
	if !got.StatusChanged || got.SEOChanged || !got.HeadersChanged || got.BodyChanged || got.Tier != TierCritical {
		t.Errorf("%+v", got)
	}
	if c := got.SEOChanges["status"]; len(c.Before) != 1 || c.Before[0] != "200" || c.After[0] != "404" {
		t.Errorf("status change = %+v", c)
	}
}
