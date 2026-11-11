package diff

// Tier ranks a Diff by significance.
type Tier string

const (
	TierCritical Tier = "critical"
	TierSEO      Tier = "seo"
	TierHeaders  Tier = "headers"
	TierBody     Tier = "body"
	TierEmpty    Tier = "empty"
)

// Rank orders tiers for the Report: lower comes first.
func (t Tier) Rank() int {
	switch t {
	case TierCritical:
		return 0
	case TierSEO:
		return 1
	case TierHeaders:
		return 2
	case TierBody:
		return 3
	}
	return 4
}

// criticalFields are the SEO fields whose change is critical whatever else changed.
var criticalFields = map[string]bool{
	"meta_robots": true, "x_robots_tag": true, "canonical": true, "redirect_target": true,
}

// Classify applies the fixed tier rule: critical = status class, meta robots,
// X-Robots-Tag, canonical or redirect target changed; seo = any other SEO
// field changed; headers = only headers changed; body = only the body changed.
func Classify(seoChanges map[string]SEOChange, headersChanged, bodyChanged bool) Tier {
	for k := range seoChanges {
		if criticalFields[k] {
			return TierCritical
		}
	}
	if s, ok := seoChanges["status"]; ok && statusClass(s.Before) != statusClass(s.After) {
		return TierCritical
	}
	switch {
	case len(seoChanges) > 0:
		return TierSEO
	case headersChanged:
		return TierHeaders
	case bodyChanged:
		return TierBody
	}
	return TierEmpty
}

// statusClass is the first digit of the status value ("" when absent).
func statusClass(vs []string) string {
	if len(vs) == 0 || vs[0] == "" {
		return ""
	}
	return vs[0][:1]
}
