package web

import (
	"fmt"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
)

// projectCrumbs is the trail of a screen under a Project:
// Projects › domain › tail. Every crumb but the last links back; the last
// crumb of tail is the current screen.
func projectCrumbs(p dbgen.Project, tail ...crumb) []crumb {
	cs := []crumb{{Label: "Projects", Href: "/"}, {Label: p.Domain, Href: fmt.Sprintf("/p/%d", p.ID)}}
	return append(cs, tail...)
}

// checkCrumbs is the trail of a screen under a Check:
// Projects › domain › Checks › Check #n › tail. With an empty tail the Check
// itself is the current screen.
func checkCrumbs(p dbgen.Project, checkID int64, tail ...crumb) []crumb {
	check := crumb{Label: fmt.Sprintf("Check #%d", checkID)}
	if len(tail) > 0 {
		check.Href = fmt.Sprintf("/c/%d", checkID)
	}
	return projectCrumbs(p, append([]crumb{{Label: "Checks", Href: fmt.Sprintf("/p/%d", p.ID)}, check}, tail...)...)
}
