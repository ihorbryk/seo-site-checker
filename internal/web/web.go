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
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/ihorbryk/seo-site-checker/internal/user"
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

// Options configures the web handler beyond its database and River client.
type Options struct {
	// Argon2 are the cost parameters new password hashes are made with.
	Argon2 user.Params
	// InsecureCookies (INSECURE_COOKIES=true) serves a plain "session" cookie
	// without Secure and sends no HSTS, for local development over HTTP.
	InsecureCookies bool
	// DenyRegistration (DENY_REGISTRATION=true) leaves /register unrouted.
	DenyRegistration bool
	// TrustedProxyHeader (TRUSTED_PROXY_HEADER) names the header whose
	// rightmost entry is the client IP for rate limits; empty uses RemoteAddr.
	TrustedProxyHeader string
	// Now is the clock for sessions and rate limits; nil means time.Now.
	Now func() time.Time
}

type server struct {
	pool   *pgxpool.Pool
	river  *river.Client[pgx.Tx] // insert-only
	q      *dbgen.Queries
	tmpl   *template.Template
	log    *slog.Logger
	opts   Options
	hasher *user.Hasher
	limits limits
	static http.Handler
	// dummyHash is verified against when a sign-in names an unknown email, so
	// timing does not tell which emails have accounts.
	dummyHash string
}

func (s *server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// New builds the web handler: the route table wrapped in the middleware chain.
// rc is an insert-only River client (check.NewInsertClient) used to start Checks.
func New(pool *pgxpool.Pool, rc *river.Client[pgx.Tx], log *slog.Logger, opts Options) (http.Handler, error) {
	tmpl, err := ParseTemplates()
	if err != nil {
		return nil, err
	}
	s := &server{pool: pool, river: rc, q: dbgen.New(pool), tmpl: tmpl, log: log, opts: opts, hasher: user.NewHasher(opts.Argon2)}
	s.limits = newLimits(s.now)
	if s.dummyHash, err = s.hasher.Hash("not anyone's password"); err != nil {
		return nil, err
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	s.static = http.StripPrefix("/static/", http.FileServerFS(static))
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.pattern, s.guard(rt.access, rt.handler))
	}
	mws := []middleware{recoverPanics(log), logRequests(log)}
	if !opts.InsecureCookies {
		mws = append([]middleware{hsts}, mws...)
	}
	return chain(mux, mws...), nil
}

func (s *server) home(w http.ResponseWriter, r *http.Request) {
	projects, err := s.q.ListProjectSummaries(r.Context(), currentUser(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "home", "Projects", []crumb{{Label: "Projects"}}, map[string]any{"Projects": projects})
}

func (s *server) addProject(w http.ResponseWriter, r *http.Request) {
	if _, err := project.Add(r.Context(), s.pool, currentUser(r).ID, r.PostFormValue("domain")); err != nil {
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
		"UserEmail": currentUser(r).Email,
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
