package web

import "net/http"

// access is who may reach a route. Every route declares one in the table
// below, so a new route cannot ship unprotected by omission. (Project-scoped
// routes get an owned(kind) level in a later ticket.)
type access int

const (
	// public: no session needed (sign-in, static files).
	public access = iota
	// signedIn: any signed-in User; signed-out requests are sent to /login.
	signedIn
)

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
	return []route{
		{"GET /static/", public, s.static},
		{"GET /login", public, h(s.loginPage)},
		{"POST /login", public, h(s.login)},

		{"POST /logout", signedIn, h(s.logout)},
		{"GET /password", signedIn, h(s.passwordPage)},
		{"POST /password", signedIn, h(s.changePassword)},
		{"GET /{$}", signedIn, h(s.home)},
		{"POST /projects", signedIn, h(s.addProject)},
		{"GET /p/{id}", signedIn, h(s.projectChecks)},
		{"POST /p/{id}/checks", signedIn, h(s.runCheck)},
		{"GET /p/{id}/settings", signedIn, h(s.settingsPage)},
		{"POST /p/{id}/settings", signedIn, h(s.saveSettings)},
		{"POST /p/{id}/settings/noise", signedIn, h(s.saveNoiseRules)},
		{"GET /p/{id}/settings/header-row", signedIn, h(s.headerRow)},
		{"GET /p/{id}/pages", signedIn, h(s.projectPages)},
		{"POST /p/{id}/pages", signedIn, h(s.addPage)},
		{"GET /pg/{id}", signedIn, h(s.pageHistory)},
		{"POST /pg/{id}/ignore", signedIn, h(s.ignorePage)},
		{"POST /pg/{id}/restore", signedIn, h(s.restorePage)},
		{"GET /c/{id}", signedIn, h(s.checkPage)},
		{"POST /c/{id}/cancel", signedIn, h(s.cancelCheck)},
		{"GET /c/{id}/progress", signedIn, h(s.progressFragment)},
		{"GET /c/{id}/sections/{section}", signedIn, h(s.reportSectionPage)},
		{"GET /s/{id}", signedIn, h(s.snapshotPage)},
		{"GET /d/{id}", signedIn, h(s.diffPage)},
	}
}
