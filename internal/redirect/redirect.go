// Package redirect classifies a 3xx response's Location and rebuilds
// redirect chains from a Check's Snapshots.
package redirect

import (
	"net/url"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/urlnorm"
)

// Kind says where a redirect target points.
type Kind int

const (
	None      Kind = iota // no usable target
	OnDomain              // exact host, https: fetched in the same Check
	OffDomain             // another host, https: recorded only
	Insecure              // http (any host): recorded only
)

// Target is a redirect's resolved, normalised Location. An http target keeps
// its http scheme, so that it stays visible as such.
type Target struct {
	URL  string
	Kind Kind
}

// Classify reads the Location of a 301, 302, 303, 307 or 308 response for the
// page at pageURL (normalised) on the Project's host. Any other status, and a
// missing, empty or unparseable Location, give the zero Target.
func Classify(status int, headers [][2]string, pageURL, host string) Target {
	switch status {
	case 301, 302, 303, 307, 308:
	default:
		return Target{}
	}
	location, found := "", false
	for _, h := range headers {
		if strings.EqualFold(h[0], "location") {
			location, found = strings.TrimSpace(h[1]), true
			break
		}
	}
	if !found || location == "" {
		return Target{}
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return Target{}
	}
	ref, err := url.Parse(location)
	if err != nil {
		return Target{}
	}
	abs := base.ResolveReference(ref)
	insecure := abs.Scheme == "http"
	norm, err := urlnorm.Normalize(abs.String())
	if err != nil {
		return Target{}
	}
	switch {
	case insecure:
		return Target{URL: "http://" + strings.TrimPrefix(norm, "https://"), Kind: Insecure}
	case urlnorm.Host(norm) == host:
		return Target{URL: norm, Kind: OnDomain}
	}
	return Target{URL: norm, Kind: OffDomain}
}
