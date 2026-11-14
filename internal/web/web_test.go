package web_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/ihorbryk/seo-site-checker/internal/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testApp is a web handler on a private database with Users to sign in as.
type testApp struct {
	// Anonymous is the handler itself: requests carry no session cookie.
	Anonymous http.Handler
	Pool      *pgxpool.Pool
	// User is the default User newServer signs in as.
	User dbgen.User
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	pool := testdb.New(t)
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	h, err := web.New(pool, rc, slog.New(slog.NewTextHandler(io.Discard, nil)), web.Options{Argon2: testfixtures.Argon2})
	if err != nil {
		t.Fatal(err)
	}
	return &testApp{Anonymous: h, Pool: pool, User: testfixtures.User(t, pool)}
}

// As returns the handler as a browser signed in as u (a fresh session).
func (a *testApp) As(t *testing.T, u dbgen.User) http.Handler {
	t.Helper()
	cookie := &http.Cookie{Name: web.CookieName(false), Value: testfixtures.Session(t, a.Pool, u)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookie)
		a.Anonymous.ServeHTTP(w, r)
	})
}

// newServer returns the handler as the default User's browser.
func newServer(t *testing.T) http.Handler {
	t.Helper()
	a := newTestApp(t)
	return a.As(t, a.User)
}

func do(t *testing.T, h http.Handler, method, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func doc(t *testing.T, w *httptest.ResponseRecorder) *goquery.Document {
	t.Helper()
	d, err := goquery.NewDocumentFromReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEveryTemplateParses(t *testing.T) {
	tmpl, err := web.ParseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shell", "authDoc", "loginForm", "home", "newProject", "project", "progress", "checkRunning", "report", "settings", "headerRow"} {
		if tmpl.Lookup(name) == nil {
			t.Errorf("template %q is not defined", name)
		}
	}
}

func TestProjectListIsEmptyAtFirstWithAddForm(t *testing.T) {
	w := do(t, newServer(t), "GET", "/", nil, false)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	d := doc(t, w)
	if d.Find("h1").Text() != "Projects" {
		t.Errorf("h1 = %q", d.Find("h1").Text())
	}
	if d.Find("header .crumbs").Length() != 1 || !strings.Contains(d.Find("header .crumbs").Text(), "Projects") {
		t.Error("breadcrumb for Projects missing")
	}
	form := d.Find("form[hx-post='/projects']")
	if form.Length() != 1 || form.Find("input[name=domain]").Length() != 1 {
		t.Error("inline Add Project form missing")
	}
	if d.Find("script[src='/static/htmx.min.js']").Length() != 1 {
		t.Error("htmx not loaded")
	}
	if d.Find("table tr td").Length() != 0 {
		t.Error("no Projects yet, expected no rows")
	}
}

func TestAddingAProjectListsItWithItsSeedPage(t *testing.T) {
	h := newServer(t)

	w := do(t, h, "POST", "/projects", url.Values{"domain": {" Example.com "}}, true)
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("htmx add: status %d, HX-Redirect %q", w.Code, w.Header().Get("HX-Redirect"))
	}

	d := doc(t, do(t, h, "GET", "/", nil, false))
	row := d.Find("table tr").Eq(1)
	if got := strings.TrimSpace(row.Find("td").Eq(0).Text()); got != "example.com" {
		t.Errorf("domain cell = %q", got)
	}
	if got := strings.TrimSpace(row.Find("td").Eq(1).Text()); got != "daily" {
		t.Errorf("schedule cell = %q", got)
	}
	if got := strings.TrimSpace(row.Find("td").Eq(2).Text()); got != "1 active" {
		t.Errorf("pages cell = %q (the seed Page)", got)
	}
}

func TestAddingWithoutHtmxRedirectsBackToTheList(t *testing.T) {
	w := do(t, newServer(t), "POST", "/projects", url.Values{"domain": {"example.com"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("status %d, Location %q", w.Code, w.Header().Get("Location"))
	}
}

func TestAddingRejectsInvalidAndDuplicateDomains(t *testing.T) {
	h := newServer(t)
	for name, domain := range map[string]string{"empty": "", "path": "example.com/x", "scheme": "http://example.com"} {
		w := do(t, h, "POST", "/projects", url.Values{"domain": {domain}}, true)
		if w.Code != http.StatusUnprocessableEntity || doc(t, w).Find(".flash.err").Length() != 1 {
			t.Errorf("%s: status %d, body %q", name, w.Code, w.Body.String())
		}
	}

	do(t, h, "POST", "/projects", url.Values{"domain": {"example.com"}}, true)
	w := do(t, h, "POST", "/projects", url.Values{"domain": {"EXAMPLE.com"}}, true)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(doc(t, w).Find(".flash.err").Text(), "already exists") {
		t.Errorf("duplicate: status %d, body %q", w.Code, w.Body.String())
	}

	rows := doc(t, do(t, h, "GET", "/", nil, false)).Find("table tr").Length()
	if rows != 2 { // header + one Project
		t.Errorf("want only the first Project listed, got %d table rows", rows)
	}
}

func TestStaticHtmxIsServed(t *testing.T) {
	w := do(t, newServer(t), "GET", "/static/htmx.min.js", nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "htmx") {
		t.Fatalf("status %d", w.Code)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	if w := do(t, newServer(t), "GET", "/nope", nil, false); w.Code != 404 {
		t.Fatalf("status %d", w.Code)
	}
}
