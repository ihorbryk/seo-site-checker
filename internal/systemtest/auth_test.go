package systemtest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/ihorbryk/seo-site-checker/internal/user"
)

func credentials(u dbgen.User, next string) url.Values {
	return url.Values{"email": {u.Email}, "password": {testfixtures.Password}, "next": {next}}
}

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-session" || c.Name == "session" {
			return c
		}
	}
	return nil
}

func sessionCount(t *testing.T, s *systemtest.System, u dbgen.User) int64 {
	t.Helper()
	n, err := s.Q.CountSessionsByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSignedOutVisitorIsSentToSignInAndBackToWhereTheyWere(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()

	w := visitor.Get("/p/7/settings?tab=noise")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?next=%2Fp%2F7%2Fsettings%3Ftab%3Dnoise" {
		t.Fatalf("status %d, Location %q", w.Code, w.Header().Get("Location"))
	}

	login := s.Page(visitor.Get(w.Header().Get("Location")))
	if got := strings.TrimSpace(login.Find(".notice").Text()); got != "Sign in to continue." {
		t.Errorf("notice = %q", got)
	}
	if next, _ := login.Find("input[name=next]").Attr("value"); next != "/p/7/settings?tab=noise" {
		t.Errorf("next field = %q", next)
	}

	w = visitor.PostPlain("/login", credentials(s.User, "/p/7/settings?tab=noise"))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/p/7/settings?tab=noise" {
		t.Fatalf("sign-in: status %d, Location %q", w.Code, w.Header().Get("Location"))
	}
	if got := visitor.Get("/"); got.Code != 200 {
		t.Fatalf("signed in, GET /: status %d", got.Code)
	}
}

func TestSignInOnlyFollowsLocalNextPaths(t *testing.T) {
	s := systemtest.New(t)
	for next, want := range map[string]string{
		"":                       "/",
		"/p/1":                   "/p/1",
		"//evil.example/x":       "/",
		"https://evil.example/x": "/",
		"/\\evil.example":        "/",
		"p/1":                    "/",
	} {
		w := s.SignedOut().PostPlain("/login", credentials(s.User, next))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != want {
			t.Errorf("next %q: status %d, Location %q; want %q", next, w.Code, w.Header().Get("Location"), want)
		}
	}
}

func TestHtmxSignInRedirectsTheWholeBrowser(t *testing.T) {
	s := systemtest.New(t)
	w := s.SignedOut().Post("/login", credentials(s.User, "/p/1"))
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/p/1" {
		t.Fatalf("status %d, HX-Redirect %q", w.Code, w.Header().Get("HX-Redirect"))
	}
}

func TestSignedOutHtmxRequestGets401AndAWholeBrowserRedirect(t *testing.T) {
	s := systemtest.New(t)
	w := s.SignedOut().Request("POST", "/projects", url.Values{"domain": {"a.example"}}, http.Header{
		"HX-Request":     {"true"},
		"HX-Current-URL": {"http://127.0.0.1:8089/p/3/pages"},
	})
	if w.Code != http.StatusUnauthorized || w.Header().Get("HX-Redirect") != "/login?next=%2Fp%2F3%2Fpages" {
		t.Fatalf("status %d, HX-Redirect %q", w.Code, w.Header().Get("HX-Redirect"))
	}
	if w.Body.Len() != 0 {
		t.Errorf("body %q, want none", w.Body.String())
	}
}

func TestWrongCredentialsGetOneGenericMessageAndKeepTheEmail(t *testing.T) {
	s := systemtest.New(t)
	for name, form := range map[string]url.Values{
		"wrong password": {"email": {s.User.Email}, "password": {"not the password at all"}},
		"unknown email":  {"email": {"nobody@example.test"}, "password": {testfixtures.Password}},
	} {
		w := s.SignedOut().PostPlain("/login", form)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d", name, w.Code)
		}
		d := s.Page(w)
		if got := strings.TrimSpace(d.Find(".alert-error").Text()); got != "Wrong email or password." {
			t.Errorf("%s: message %q", name, got)
		}
		if got, _ := d.Find("input[name=email]").Attr("value"); got != form.Get("email") {
			t.Errorf("%s: email field %q not kept", name, got)
		}
		if d.Find("input[name=password]").AttrOr("value", "") != "" {
			t.Errorf("%s: the password must never be echoed", name)
		}
		if sessionCookie(w) != nil {
			t.Errorf("%s: no cookie on failure", name)
		}
	}

	// htmx swaps the form itself: the fragment, not a document.
	w := s.SignedOut().Post("/login", url.Values{"email": {s.User.Email}, "password": {"x"}})
	if w.Code != http.StatusUnprocessableEntity || strings.Contains(w.Body.String(), "<html") ||
		!strings.Contains(w.Body.String(), "Wrong email or password.") {
		t.Errorf("htmx failure: status %d, body %q", w.Code, w.Body.String())
	}

	w = s.SignedOut().PostPlain("/login", url.Values{"email": {" "}, "password": {""}})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Enter your email and password.") {
		t.Errorf("empty form: status %d, body %q", w.Code, w.Body.String())
	}
}

func TestEmailMatchesRegardlessOfCaseAndSpaces(t *testing.T) {
	s := systemtest.New(t)
	b := s.SignedOut()
	form := credentials(s.User, "")
	form.Set("email", "  "+strings.ToUpper(s.User.Email)+" ")
	if w := b.PostPlain("/login", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
}

func TestSignInShowsTheLoginScreenOutsideTheShell(t *testing.T) {
	s := systemtest.New(t)
	d := s.Page(s.SignedOut().Get("/login"))
	if d.Find("header.navbar").Length() != 0 || d.Find("form[action='/login'] input[name=email][autocomplete=username]").Length() != 1 ||
		d.Find("form[action='/login'] input[name=password][autocomplete=current-password]").Length() != 1 {
		t.Error("the login form is missing or sits inside the app shell")
	}
	if d.Find("input[type=checkbox]").Length() != 1 || !strings.Contains(d.Text(), "Show password") {
		t.Error("show-password toggle missing")
	}
	if d.Find(".notice").Length() != 0 {
		t.Error("no notice without next or a sign-out")
	}
	if !strings.Contains(d.Text(), "Know when a deploy changes what search engines see.") {
		t.Error("split layout pitch missing")
	}
}

func TestHeaderShowsTheEmailAndSignOutEndsTheSession(t *testing.T) {
	s := systemtest.New(t)
	d := s.Page(s.Get("/"))
	if got := strings.TrimSpace(d.Find("header button.btn-ghost").First().Text()); got != s.User.Email {
		t.Errorf("header email = %q, want %q", got, s.User.Email)
	}
	if d.Find("header form[action='/logout'][method=post] button[aria-label='Sign out']").Length() != 1 {
		t.Error("sign-out button missing")
	}

	w := s.Browser.PostPlain("/logout", url.Values{})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?signedout=1" {
		t.Fatalf("logout: status %d, Location %q", w.Code, w.Header().Get("Location"))
	}
	if c := sessionCookie(w); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout must clear the cookie, got %+v", c)
	}
	if n := sessionCount(t, s, s.User); n != 0 {
		t.Errorf("%d sessions left", n)
	}
	if got := s.Get("/"); got.Code != http.StatusSeeOther {
		t.Errorf("signed out, GET /: status %d", got.Code)
	}
	if got := strings.TrimSpace(s.Page(s.Get("/login?signedout=1")).Find(".notice").Text()); got != "You've signed out." {
		t.Errorf("notice = %q", got)
	}
}

func TestSigningInAgainReplacesTheSessionTheBrowserHeld(t *testing.T) {
	s := systemtest.New(t)
	old := s.Browser.Token()
	if w := s.Browser.PostPlain("/login", credentials(s.User, "")); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if s.Browser.Token() == old {
		t.Fatal("sign-in must issue a new token")
	}
	if n := sessionCount(t, s, s.User); n != 1 {
		t.Fatalf("%d sessions, want only the new one", n)
	}
}

func TestSessionCookieAttributesAndHSTS(t *testing.T) {
	s := systemtest.New(t)
	w := s.SignedOut().PostPlain("/login", credentials(s.User, ""))
	c := sessionCookie(w)
	if c == nil || c.Name != "__Host-session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode ||
		c.Path != "/" || c.MaxAge != 30*24*60*60 || c.Domain != "" || c.Value == "" {
		t.Fatalf("cookie = %+v", c)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS header missing")
	}
	if got := s.SignedOut().Get("/static/htmx.min.js").Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS belongs on every response")
	}
}

func TestInsecureCookiesGivesAPlainCookieAndNoHSTS(t *testing.T) {
	s := systemtest.New(t, systemtest.InsecureCookies())
	w := s.SignedOut().PostPlain("/login", credentials(s.User, ""))
	c := sessionCookie(w)
	if c == nil || c.Name != "session" || c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie = %+v", c)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS = %q, want none", got)
	}
	if s.Get("/").Code != 200 {
		t.Error("the default Browser must be signed in under INSECURE_COOKIES too")
	}
}

func TestSigningInUpgradesAHashMadeWithOldParameters(t *testing.T) {
	s := systemtest.New(t)
	old, err := user.NewHasher(user.Params{MemoryKiB: 32, Time: 1, Threads: 1}).Hash(testfixtures.Password)
	if err != nil {
		t.Fatal(err)
	}
	s.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, old, s.User.ID)

	if w := s.SignedOut().PostPlain("/login", credentials(s.User, "")); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	var hash string
	if err := s.Pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, s.User.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	ok, needsRehash := testfixtures.Hasher().Verify(testfixtures.Password, hash)
	if !ok || needsRehash {
		t.Fatalf("hash after sign-in: verifies=%v needsRehash=%v", ok, needsRehash)
	}
}

func TestALockedUserCannotSignIn(t *testing.T) {
	s := systemtest.New(t)
	s.Exec(`UPDATE users SET password_hash = '!' WHERE id = $1`, s.User.ID)
	if w := s.SignedOut().PostPlain("/login", credentials(s.User, "")); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", w.Code)
	}
}

func TestExpiredSessionIsSignedOut(t *testing.T) {
	s := systemtest.New(t)
	s.Exec(`UPDATE sessions SET last_seen_at = $1 WHERE user_id = $2`, time.Now().Add(-15*24*time.Hour), s.User.ID)
	if w := s.Get("/"); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want redirect to sign-in", w.Code)
	}
}

func TestEachUserSeesOnlyTheirOwnProjects(t *testing.T) {
	s := systemtest.New(t)
	other := s.NewUser()

	s.AddProject("mine.example")
	other.AddProject("theirs.example")
	other.AddProject("mine.example") // the same domain works under two Users

	domains := func(b *systemtest.Browser) string {
		var got []string
		s.Page(b.Get("/")).Find("table td:first-child a").Each(func(_ int, a *goquery.Selection) {
			got = append(got, strings.TrimSpace(a.Text()))
		})
		return strings.Join(got, ",")
	}
	if got := domains(s.Browser); got != "mine.example" {
		t.Errorf("first User sees %q", got)
	}
	if got := domains(other); got != "mine.example,theirs.example" {
		t.Errorf("second User sees %q", got)
	}

	// The duplicate-domain error is per owner.
	w := s.Post("/projects", url.Values{"domain": {"mine.example"}})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "already exists") {
		t.Errorf("duplicate under the same owner: status %d, body %q", w.Code, w.Body.String())
	}
}
