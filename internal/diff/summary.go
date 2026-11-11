package diff

import (
	"bytes"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/seo"
)

// Side is one Snapshot as a Diff sees it. Body is loaded lazily through
// LoadBody so that equal hashes never cost a read.
type Side struct {
	Headers    [][2]string
	SEO        seo.Fields
	BodySHA256 []byte // nil when the Snapshot has no body
	LoadBody   func() ([]byte, error)
}

// Summary is the precomputed result of a Diff.
type Summary struct {
	StatusChanged  bool
	HeadersChanged bool
	SEOChanged     bool // an SEO field other than status changed
	BodyChanged    bool
	SEOChanges     map[string]SEOChange
	Tier           Tier
}

// Summarize compares a Snapshot with its baseline under the Noise rules.
// Equal body hashes skip the body comparison; a body on one side only is a
// change; otherwise the bodies are decoded, reformatted and masked, and
// changed when the results differ.
func Summarize(before, after Side, rules Rules) (Summary, error) {
	s := Summary{SEOChanges: CompareSEO(before.SEO, after.SEO)}
	_, s.StatusChanged = s.SEOChanges["status"]
	for k := range s.SEOChanges {
		if k != "status" {
			s.SEOChanged = true
		}
	}
	s.HeadersChanged = len(CompareHeaders(before.Headers, after.Headers, rules.IgnoredHeaders())) > 0
	body, err := bodyChanged(before, after, rules)
	if err != nil {
		return Summary{}, err
	}
	s.BodyChanged = body
	s.Tier = Classify(s.SEOChanges, s.HeadersChanged, s.BodyChanged)
	return s, nil
}

func bodyChanged(before, after Side, rules Rules) (bool, error) {
	switch {
	case before.BodySHA256 == nil && after.BodySHA256 == nil:
		return false, nil
	case before.BodySHA256 == nil || after.BodySHA256 == nil:
		return true, nil
	case bytes.Equal(before.BodySHA256, after.BodySHA256):
		return false, nil
	}
	b, err := MaskedBody(before.Headers, before.LoadBody, rules)
	if err != nil {
		return false, err
	}
	a, err := MaskedBody(after.Headers, after.LoadBody, rules)
	if err != nil {
		return false, err
	}
	return a != b, nil
}

// MaskedBody loads a body and returns it decoded, reformatted and masked, the
// form that is line-diffed.
func MaskedBody(headers [][2]string, load func() ([]byte, error), rules Rules) (string, error) {
	raw, err := load()
	if err != nil {
		return "", err
	}
	ct := ""
	for _, h := range headers {
		if strings.EqualFold(h[0], "content-type") {
			ct = h[1]
			break
		}
	}
	return ApplyMasks(Reformat(seo.Decode(ct, raw)), rules.Masks()), nil
}
