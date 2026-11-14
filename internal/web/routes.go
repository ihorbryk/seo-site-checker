package web

import "net/http"

// accessLevel is how far a request must get before its handler runs.
type accessLevel int

const (
	publicLevel accessLevel = iota
	signedInLevel
	ownedLevel
)

// ownerKind is the kind of row an owned route's {id} names; its Project's
// owner is who may reach the route.
type ownerKind int

const (
	projectKind ownerKind = iota
	pageKind
	checkKind
	snapshotKind
)

// access is who may reach a route. Every route declares one in the table
// below, so a new route cannot ship unprotected by omission.
type access struct {
	level accessLevel
	kind  ownerKind // owned routes only
}

var (
	// public: no session needed (sign-in, static files).
	public = access{level: publicLevel}
	// signedIn: any signed-in User; signed-out requests are sent to /login.
	signedIn = access{level: signedInLevel}
)

// owned: signedIn, and the {id} must name a row of kind in a Project the User
// owns. A missing row and another User's row both answer 404.
func owned(kind ownerKind) access { return access{level: ownedLevel, kind: kind} }

func (a access) isOwned() bool { return a.level == ownedLevel }

// route is one row of the route table: a ServeMux pattern, its access level
// and the handler.
type route struct {
	pattern string
	access  access
	handler http.Handler
}

// routes is the one place routes are declared.
func (s *server) routes() []route {
	h := func(f http.HandlerFunc) http.Handler { return f }
	rs := []route{
		{"GET /static/", public, s.static},
		{"GET /login", public, h(s.loginPage)},
		{"POST /login", public, h(s.login)},

		{"POST /logout", signedIn, h(s.logout)},
		{"GET /{$}", signedIn, h(s.home)},
		{"POST /projects", signedIn, h(s.addProject)},
		{"GET /p/{id}", owned(projectKind), h(s.projectChecks)},
		{"POST /p/{id}/checks", owned(projectKind), h(s.runCheck)},
		{"GET /p/{id}/settings", owned(projectKind), h(s.settingsPage)},
		{"POST /p/{id}/settings", owned(projectKind), h(s.saveSettings)},
		{"POST /p/{id}/settings/noise", owned(projectKind), h(s.saveNoiseRules)},
		{"GET /p/{id}/settings/header-row", owned(projectKind), h(s.headerRow)},
		{"GET /p/{id}/pages", owned(projectKind), h(s.projectPages)},
		{"POST /p/{id}/pages", owned(projectKind), h(s.addPage)},
		{"GET /pg/{id}", owned(pageKind), h(s.pageHistory)},
		{"POST /pg/{id}/ignore", owned(pageKind), h(s.ignorePage)},
		{"POST /pg/{id}/restore", owned(pageKind), h(s.restorePage)},
		{"GET /c/{id}", owned(checkKind), h(s.checkPage)},
		{"POST /c/{id}/cancel", owned(checkKind), h(s.cancelCheck)},
		{"GET /c/{id}/progress", owned(checkKind), h(s.progressFragment)},
		{"GET /c/{id}/sections/{section}", owned(checkKind), h(s.reportSectionPage)},
		{"GET /s/{id}", owned(snapshotKind), h(s.snapshotPage)},
		{"GET /d/{id}", owned(snapshotKind), h(s.diffPage)},
	}
	if !s.opts.DenyRegistration {
		rs = append(rs,
			route{"GET /register", public, h(s.registerPage)},
			route{"POST /register", public, h(s.register)},
		)
	}
	return rs
}
