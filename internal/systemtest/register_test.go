package systemtest_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

func registration(email, password string) url.Values {
	return url.Values{"email": {email}, "password": {password}}
}

func TestRegisteringSignsTheVisitorInOnAnEmptyProjectList(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()

	form := s.Page(visitor.Get("/register"))
	if form.Find("form[action='/register'] input[name=email][autocomplete=username]").Length() != 1 ||
		form.Find("form[action='/register'] input[name=password][autocomplete=new-password][minlength='15']").Length() != 1 ||
		form.Find("input[type=password]").Length() != 1 {
		t.Error("the register form is missing its email and single new-password field")
	}
	if got := form.Find("form[action='/register']").Text(); !strings.Contains(got, "At least 15 characters. A few unrelated words work well.") {
		t.Error("the password hint is missing")
	}
	if form.Find("a[href='/login']").Length() == 0 || !strings.Contains(form.Text(), "Already have an account?") {
		t.Error("no link back to sign in")
	}

	w := visitor.PostPlain("/register", registration("  New@Example.Test ", testfixtures.Password))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("status %d, Location %q", w.Code, w.Header().Get("Location"))
	}
	if visitor.Token() == "" {
		t.Fatal("registering must sign the visitor in")
	}
	home := s.Page(visitor.Get("/"))
	if home.Find("header").Length() == 0 || home.Find("a[href^='/p/']").Length() != 0 {
		t.Error("want the signed-in, empty Project list")
	}
}

func TestRegisteringOverHTMXRedirects(t *testing.T) {
	s := systemtest.New(t)
	w := s.SignedOut().Post("/register", registration("htmx@example.test", testfixtures.Password))
	if w.Header().Get("HX-Redirect") != "/" {
		t.Errorf("HX-Redirect = %q (status %d)", w.Header().Get("HX-Redirect"), w.Code)
	}
}

func TestRegisteringReplacesTheSessionTheBrowserHeld(t *testing.T) {
	s := systemtest.New(t)
	old := s.Browser.Token()
	if w := s.Browser.PostPlain("/register", registration("another@example.test", testfixtures.Password)); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if s.Browser.Token() == old || sessionCount(t, s, s.User) != 0 {
		t.Error("the held session must be replaced")
	}
}

func TestRegistrationValidationErrorsAreInlineAndKeepTheEmail(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()

	w := visitor.Post("/register", registration("kept@example.test", "short"))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", w.Code)
	}
	d := s.Page(w)
	if got := strings.TrimSpace(d.Find("[data-field=password]").Text()); got != "Use at least 15 characters (this one has 5)." {
		t.Errorf("password error = %q", got)
	}
	if v, _ := d.Find("input[name=email]").Attr("value"); v != "kept@example.test" {
		t.Errorf("email = %q", v)
	}
	if d.Find("[data-field=email]").Length() != 0 {
		t.Error("email has no error")
	}

	d = s.Page(visitor.Post("/register", registration("not-an-email", testfixtures.Password)))
	if got := strings.TrimSpace(d.Find("[data-field=email]").Text()); got != "Enter an email address like name@example.com." {
		t.Errorf("email error = %q", got)
	}
	if visitor.Token() != "" {
		t.Error("a refused registration must not sign in")
	}
}

func TestRegisteringATakenEmailSaysSo(t *testing.T) {
	s := systemtest.New(t)
	w := s.SignedOut().PostPlain("/register", registration(strings.ToUpper(s.User.Email), testfixtures.Password))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", w.Code)
	}
	if got := strings.TrimSpace(s.Page(w).Find("[data-field=email]").Text()); got != "An account with this email already exists. Sign in instead." {
		t.Errorf("error = %q", got)
	}
}

func TestSignInLinksToRegistration(t *testing.T) {
	s := systemtest.New(t)
	d := s.Page(s.SignedOut().Get("/login"))
	if d.Find("a[href='/register']").Text() != "Create one" {
		t.Error("login lacks the Create one link")
	}
}

func TestWithRegistrationDeniedThereIsNoRegisterRoute(t *testing.T) {
	s := systemtest.New(t, systemtest.DenyRegistration())
	visitor := s.SignedOut()
	if w := visitor.Get("/register"); w.Code != http.StatusNotFound {
		t.Errorf("GET /register: status %d", w.Code)
	}
	if w := visitor.PostPlain("/register", registration("x@example.test", testfixtures.Password)); w.Code != http.StatusNotFound {
		t.Errorf("POST /register: status %d", w.Code)
	}
	d := s.Page(visitor.Get("/login"))
	if d.Find("a[href='/register']").Length() != 0 || !strings.Contains(d.Text(), "Accounts are created by the operator of this site.") {
		t.Error("login should explain accounts are created by the operator")
	}
}
