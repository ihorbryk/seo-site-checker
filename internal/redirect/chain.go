package redirect

import "strings"

// MaxHops is how many redirects a rebuilt chain follows.
const MaxHops = 10

// Snap is what chain rebuilding needs from a Page's Snapshot in the Check.
type Snap struct {
	Status int
	Target string // the redirect_target SEO field; empty when none
}

// Stop says why a chain ends.
type Stop int

const (
	StopFinal     Stop = iota // reached a Snapshot that does not redirect
	StopOffDomain             // the last URL is off-domain: not fetched
	StopInsecure              // the last URL is http: not fetched
	StopMissing               // the last URL has no Snapshot in the Check
	StopLoop                  // the last URL was already in the chain
	StopMaxHops               // 10 redirects followed and it still goes on
)

// Chain is the URLs a redirect passes through, from the first URL to the last.
type Chain struct {
	URLs []string
	Stop Stop
}

// Redirects reports whether the chain followed at least one redirect.
func (c Chain) Redirects() bool { return len(c.URLs) > 1 }

// BuildChain follows redirect targets from start through one Check's
// Snapshots (looked up by URL), for the Project's host. It stops at a
// Snapshot with no target, an off-domain or http target, a URL with no
// Snapshot, a loop (the repeated URL is the last one) or after MaxHops.
// Anything that can rebuild a chain (the Report, Page history) uses it.
func BuildChain(start, host string, lookup func(url string) (Snap, bool)) Chain {
	c := Chain{URLs: []string{start}}
	seen := map[string]bool{start: true}
	cur := start
	for {
		snap, ok := lookup(cur)
		switch {
		case !ok:
			c.Stop = StopMissing
			return c
		case snap.Target == "":
			c.Stop = StopFinal
			return c
		case len(c.URLs) > MaxHops:
			c.Stop = StopMaxHops
			return c
		}
		c.URLs = append(c.URLs, snap.Target)
		switch {
		case strings.HasPrefix(snap.Target, "http://"):
			c.Stop = StopInsecure
			return c
		case seen[snap.Target]:
			c.Stop = StopLoop
			return c
		case hostOf(snap.Target) != host:
			c.Stop = StopOffDomain
			return c
		}
		seen[snap.Target] = true
		cur = snap.Target
	}
}

// Label renders the chain for a Report row or Page history: the URLs joined
// by arrows, with a loop or a cut-off after 10 hops marked.
func (c Chain) Label() string {
	s := strings.Join(c.URLs, " → ")
	switch c.Stop {
	case StopLoop:
		s += " (loop)"
	case StopMaxHops:
		s += " (stopped after 10 hops)"
	}
	return s
}

func hostOf(u string) string {
	rest := strings.TrimPrefix(u, "https://")
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
