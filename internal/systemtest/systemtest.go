// Package systemtest is the in-process system harness (ADR 0003, spec
// "Testing Decisions"): the web router behind httptest, a real River worker
// and a fresh per-test database, with the worker's injected *http.Client
// dialling fakesite servers. Scenario tests drive it like a user would: add a
// Project, press Run Check, wait, read pages.
//
// Extend it by adding methods here, not by reaching into the pieces from
// tests; see docs/testing.md.
package systemtest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/ihorbryk/seo-site-checker/internal/scheduler"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/web"
	"github.com/ihorbryk/seo-site-checker/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// WaitTimeout is how long WaitForCheck polls before failing the test.
const WaitTimeout = 30 * time.Second

// System is a running web + worker on a private database.
type System struct {
	T       testing.TB
	Pool    *pgxpool.Pool
	Q       *dbgen.Queries
	Web     http.Handler
	Worker  *river.Client[pgx.Tx]
	Network *fakesite.Network
	insert  *river.Client[pgx.Tx] // the insert-only client web and the scheduler share
}

// New starts the system; everything stops when the test ends. The worker
// starts with no reachable hosts: add them with Site.
func New(t testing.TB) *System {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := testdb.New(t)
	s := &System{T: t, Pool: pool, Q: dbgen.New(pool), Network: fakesite.NewNetwork()}

	insert, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	s.insert = insert
	if s.Web, err = web.New(pool, insert, log); err != nil {
		t.Fatal(err)
	}
	s.Worker, err = worker.NewClient(pool, worker.Options{
		Concurrency:       20,
		Log:               log,
		HTTPClient:        s.Network.Client(),
		FetchPollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Worker.StopAndCancel(ctx)
	})
	return s
}

// Site starts a fake site for host (e.g. "shop.test") that the worker can reach.
func (s *System) Site(host string) *fakesite.Site {
	s.T.Helper()
	site := fakesite.New(s.T, host)
	s.Network.Add(site)
	return site
}

// AddProject adds a Project through the web form, as the user does, and
// returns it.
func (s *System) AddProject(domain string) dbgen.Project {
	s.T.Helper()
	if w := s.Post("/projects", url.Values{"domain": {domain}}); w.Code >= 400 {
		s.T.Fatalf("add project %q: status %d: %s", domain, w.Code, w.Body.String())
	}
	d, err := project.NormalizeDomain(domain)
	if err != nil {
		s.T.Fatal(err)
	}
	p, err := s.Q.GetProjectByDomain(context.Background(), d)
	if err != nil {
		s.T.Fatal(err)
	}
	return p
}

// Exec runs SQL against the test database, for setting up state that has no UI
// yet (e.g. a Project's timeout).
func (s *System) Exec(sql string, args ...any) {
	s.T.Helper()
	if _, err := s.Pool.Exec(context.Background(), sql, args...); err != nil {
		s.T.Fatalf("exec %q: %v", sql, err)
	}
}

// SaveRequestSettings submits the Project's Settings tab Request settings form
// and fails the test unless it is accepted.
func (s *System) SaveRequestSettings(p dbgen.Project, form url.Values) {
	s.T.Helper()
	if w := s.Post("/p/"+itoa(p.ID)+"/settings", form); w.Code != 200 {
		s.T.Fatalf("save settings: status %d: %s", w.Code, w.Body.String())
	}
}

// Get requests path from the web app.
func (s *System) Get(path string) *httptest.ResponseRecorder {
	s.T.Helper()
	return s.do("GET", path, nil)
}

// Post submits a form to the web app the way htmx does.
func (s *System) Post(path string, form url.Values) *httptest.ResponseRecorder {
	s.T.Helper()
	return s.do("POST", path, form)
}

func (s *System) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if method == "POST" { // forms are submitted by htmx; pages are full loads
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	s.Web.ServeHTTP(w, r)
	return w
}

// Page parses a response body as HTML for goquery assertions.
func (s *System) Page(w *httptest.ResponseRecorder) *goquery.Document {
	s.T.Helper()
	d, err := goquery.NewDocumentFromReader(strings.NewReader(w.Body.String()))
	if err != nil {
		s.T.Fatal(err)
	}
	return d
}

// Tick runs the scheduler once at the explicit now and returns the ids of the
// Checks it started (queued; the worker picks them up).
func (s *System) Tick(now time.Time) []int64 {
	s.T.Helper()
	ctx := context.Background()
	var before int64
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM checks`).Scan(&before); err != nil {
		s.T.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := scheduler.Tick(ctx, s.Pool, s.insert, now, log); err != nil {
		s.T.Fatal(err)
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM checks WHERE id > $1 ORDER BY id`, before)
	if err != nil {
		s.T.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		s.T.Fatal(err)
	}
	return ids
}

// RunCheck presses Run Check on the Project's Checks tab and returns the id of
// the Check the app sent the user to (a new one, or the already active one).
func (s *System) RunCheck(p dbgen.Project) int64 {
	s.T.Helper()
	w := s.Post("/p/"+itoa(p.ID)+"/checks", url.Values{})
	loc := w.Header().Get("HX-Redirect")
	if w.Code != 200 || !strings.HasPrefix(loc, "/c/") {
		s.T.Fatalf("run check: status %d, HX-Redirect %q", w.Code, loc)
	}
	return atoi(s.T, strings.TrimPrefix(loc, "/c/"))
}

// AddPage submits the Page pool tab's Add Page form and returns the response.
func (s *System) AddPage(p dbgen.Project, rawURL string) *httptest.ResponseRecorder {
	s.T.Helper()
	return s.Post("/p/"+itoa(p.ID)+"/pages", url.Values{"url": {rawURL}})
}

// PageID returns the id of the Project's Page with the exact normalised url.
func (s *System) PageID(p dbgen.Project, pageURL string) int64 {
	s.T.Helper()
	var id int64
	err := s.Pool.QueryRow(context.Background(), `SELECT id FROM pages WHERE project_id = $1 AND url = $2`, p.ID, pageURL).Scan(&id)
	if err != nil {
		s.T.Fatalf("no Page %q: %v", pageURL, err)
	}
	return id
}

// IgnorePage presses Ignore on the Page's history page.
func (s *System) IgnorePage(pageID int64) *httptest.ResponseRecorder {
	s.T.Helper()
	return s.Post("/pg/"+itoa(pageID)+"/ignore", url.Values{})
}

// RestorePage presses Restore on the Page's history page.
func (s *System) RestorePage(pageID int64) *httptest.ResponseRecorder {
	s.T.Helper()
	return s.Post("/pg/"+itoa(pageID)+"/restore", url.Values{})
}

// WaitForCheck polls until the Check has ended and returns its Report page.
func (s *System) WaitForCheck(id int64) *goquery.Document {
	s.T.Helper()
	deadline := time.Now().Add(WaitTimeout)
	for {
		c, err := s.Q.GetCheck(context.Background(), id)
		if err != nil {
			s.T.Fatal(err)
		}
		if !check.Active(c.State) {
			w := s.Get("/c/" + itoa(id))
			if w.Code != 200 {
				s.T.Fatalf("report of Check %d: status %d", id, w.Code)
			}
			return s.Page(w)
		}
		if time.Now().After(deadline) {
			s.T.Fatalf("Check %d still %s after %s", id, c.State, WaitTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
