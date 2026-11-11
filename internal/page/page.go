// Package page names the values a Page's state, origin and events are stored
// as, and holds the one rule about Pages that web and worker must agree on:
// the seed Page is never excluded.
package page

import "github.com/ihorbryk/seo-site-checker/internal/exclude"

// Page states.
const (
	StateActive  = "active"
	StateRetired = "retired"
	StateIgnored = "ignored"
)

// Page origins.
const (
	OriginSeed       = "seed"
	OriginDiscovered = "discovered"
	OriginManual     = "manual"
)

// Page event kinds.
const (
	EventNew         = "new"
	EventVanished    = "vanished"
	EventRetired     = "retired"
	EventReactivated = "reactivated"
)

// RetireAfter is how many consecutive orphaned Checks retire a discovered
// Page. It mirrors the constant in the CountOrphans query.
const RetireAfter = 3

// Excluded reports whether a Page of the given origin matches one of the
// exclude patterns. The seed Page is never excluded, whatever the patterns say.
// Exclusion is computed, never stored.
func Excluded(patterns []string, origin, pageURL string) bool {
	return origin != OriginSeed && exclude.Excluded(patterns, pageURL)
}
