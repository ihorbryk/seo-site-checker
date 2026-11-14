package systemtest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/ihorbryk/seo-site-checker/internal/web"
)

// Browser is one visitor of the web app: it holds the session cookie, sends
// it with every request and follows the app's Set-Cookie (sign-in, sign-out),
// like a cookie jar of one cookie. System's own Get/Post/AddProject helpers
// delegate to its default Browser (System.Browser), signed in as System.User.
//
// Responses are returned as they come: redirects are not followed.
type Browser struct {
	// User is who the Browser was signed in as at creation; zero for a
	// signed-out Browser. Signing in or out through the forms does not change it.
	User dbgen.User

	s     *System
	token string
}

// NewUser creates another User and returns a Browser signed in as them, for
// tests about two Users.
func (s *System) NewUser() *Browser {
	s.T.Helper()
	u := testfixtures.User(s.T, s.Pool)
	return &Browser{User: u, s: s, token: testfixtures.Session(s.T, s.Pool, u)}
}

// SignInAs returns another Browser (a second device) signed in as u.
func (s *System) SignInAs(u dbgen.User) *Browser {
	s.T.Helper()
	return &Browser{User: u, s: s, token: testfixtures.Session(s.T, s.Pool, u)}
}

// SignedOut returns a Browser with no session cookie.
func (s *System) SignedOut() *Browser { return &Browser{s: s} }

// Token returns the raw session token the Browser currently holds ("" when
// signed out).
func (b *Browser) Token() string { return b.token }

// Request sends a request with the given form body (nil for none) and extra
// headers, and applies the response's session Set-Cookie to the Browser.
func (b *Browser) Request(method, path string, form url.Values, header http.Header) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, vs := range header {
		for _, v := range vs {
			r.Header.Add(k, v) // Add canonicalises the key, as Header.Get expects
		}
	}
	name := web.CookieName(b.s.webOpts.InsecureCookies)
	if b.token != "" {
		r.AddCookie(&http.Cookie{Name: name, Value: b.token})
	}
	w := httptest.NewRecorder()
	b.s.Web.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name != name {
			continue
		}
		if c.MaxAge < 0 {
			b.token = ""
		} else {
			b.token = c.Value
		}
	}
	return w
}

var htmx = http.Header{"HX-Request": {"true"}}

// Get requests path as a full page load.
func (b *Browser) Get(path string) *httptest.ResponseRecorder {
	b.s.T.Helper()
	return b.Request("GET", path, nil, nil)
}

// GetHTMX requests path the way an htmx hx-get does (fragment, no shell).
func (b *Browser) GetHTMX(path string) *httptest.ResponseRecorder {
	b.s.T.Helper()
	return b.Request("GET", path, nil, htmx)
}

// Post submits a form the way htmx does.
func (b *Browser) Post(path string, form url.Values) *httptest.ResponseRecorder {
	b.s.T.Helper()
	if form == nil {
		form = url.Values{}
	}
	return b.Request("POST", path, form, htmx)
}

// PostPlain submits a form as a plain browser form post (no JavaScript, no htmx).
func (b *Browser) PostPlain(path string, form url.Values) *httptest.ResponseRecorder {
	b.s.T.Helper()
	if form == nil {
		form = url.Values{}
	}
	return b.Request("POST", path, form, nil)
}

// AddProject adds a Project through the web form and returns it, found by
// this Browser's User and domain.
func (b *Browser) AddProject(domain string) dbgen.Project {
	b.s.T.Helper()
	if w := b.Post("/projects", url.Values{"domain": {domain}}); w.Code >= 400 {
		b.s.T.Fatalf("add project %q: status %d: %s", domain, w.Code, w.Body.String())
	}
	d, err := project.NormalizeDomain(domain)
	if err != nil {
		b.s.T.Fatal(err)
	}
	p, err := b.s.Q.GetProjectByDomain(context.Background(), dbgen.GetProjectByDomainParams{OwnerID: b.User.ID, Domain: d})
	if err != nil {
		b.s.T.Fatal(err)
	}
	return p
}
