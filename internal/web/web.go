// Package web is the htmx UI: a classic multi-page app on net/http's ServeMux
// with html/template. Handlers read and write Postgres only.
package web

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// ParseTemplates parses every embedded template; the web server refuses to
// start (and a test fails) if any of them does not parse.
func ParseTemplates() (*template.Template, error) {
	return template.New("").ParseFS(templateFS, "templates/*.html")
}

type crumb struct{ Label, Href string }

type server struct {
	pool  *pgxpool.Pool
	river *river.Client[pgx.Tx] // insert-only
	q     *dbgen.Queries
	tmpl  *template.Template
	log   *slog.Logger
}

// New builds the web handler: routes wrapped in the middleware chain. rc is an
// insert-only River client (check.NewInsertClient) used to start Checks.
func New(pool *pgxpool.Pool, rc *river.Client[pgx.Tx], log *slog.Logger) (http.Handler, error) {
	tmpl, err := ParseTemplates()
	if err != nil {
		return nil, err
	}
	s := &server{pool: pool, river: rc, q: dbgen.New(pool), tmpl: tmpl, log: log}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /projects", s.addProject)
	mux.HandleFunc("GET /p/{id}", s.projectChecks)
	mux.HandleFunc("POST /p/{id}/checks", s.runCheck)
	mux.HandleFunc("GET /p/{id}/settings", s.settingsPage)
	mux.HandleFunc("POST /p/{id}/settings", s.saveSettings)
	mux.HandleFunc("POST /p/{id}/settings/noise", s.saveNoiseRules)
	mux.HandleFunc("GET /p/{id}/settings/header-row", s.headerRow)
	mux.HandleFunc("GET /p/{id}/pages", s.projectPages)
	mux.HandleFunc("POST /p/{id}/pages", s.addPage)
	mux.HandleFunc("GET /pg/{id}", s.pageHistory)
	mux.HandleFunc("POST /pg/{id}/ignore", s.ignorePage)
	mux.HandleFunc("POST /pg/{id}/restore", s.restorePage)
	mux.HandleFunc("GET /c/{id}", s.checkPage)
	mux.HandleFunc("POST /c/{id}/cancel", s.cancelCheck)
	mux.HandleFunc("GET /c/{id}/progress", s.progressFragment)
	mux.HandleFunc("GET /c/{id}/sections/{section}", s.reportSectionPage)
	mux.HandleFunc("GET /s/{id}", s.snapshotPage)
	mux.HandleFunc("GET /d/{id}", s.diffPage)
	return chain(mux, recoverPanics(log), logRequests(log)), nil
}

func (s *server) home(w http.ResponseWriter, r *http.Request) {
	projects, err := s.q.ListProjectSummaries(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "home", "Projects", []crumb{{Label: "Projects"}}, map[string]any{"Projects": projects})
}

func (s *server) addProject(w http.ResponseWriter, r *http.Request) {
	if _, err := project.Add(r.Context(), s.pool, r.PostFormValue("domain")); err != nil {
		if errors.Is(err, project.ErrInvalidDomain) || errors.Is(err, project.ErrDuplicate) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			s.fragment(w, r, "flash", map[string]any{"Err": true, "Message": err.Error()})
			return
		}
		s.fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// render writes a screen: just the screen for htmx requests (but history restores), otherwise wrapped
// in the shell with the breadcrumb header.
func (s *server) render(w http.ResponseWriter, r *http.Request, screen, title string, crumbs []crumb, data map[string]any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, screen, data); err != nil {
		s.fail(w, r, err)
		return
	}
	// A history restore (Back to a URL a Report section pushed) needs the whole page.
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		_, _ = w.Write(buf.Bytes())
		return
	}
	var page bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&page, "shell", map[string]any{
		"Title": title, "Crumbs": crumbs, "Content": template.HTML(buf.String()),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

// fragment writes a template without the shell.
func (s *server) fragment(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render fragment", "name", name, "err", err)
	}
}

func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
