package systemtest_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

const newPassword = "a brand new passphrase"

func changeForm(current, next string) url.Values {
	return url.Values{"current_password": {current}, "new_password": {next}}
}

func TestChangingThePasswordSignsOutOtherDevicesAndKeepsThisOne(t *testing.T) {
	s := systemtest.New(t)
	other := s.SignInAs(s.User) // a second device, signed in as the same User
	before := s.Browser.Token()

	w := s.Browser.Post("/password", changeForm(testfixtures.Password, newPassword))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Password changed. You&#39;re signed out on every other device.") {
		t.Fatalf("status %d, body %q", w.Code, w.Body.String())
	}
	if c := s.SessionCookie(w); c == nil || c.Value == "" || c.Value == before {
		t.Fatalf("the current session must rotate to a fresh token, got %+v", c)
	}
	if s.Get("/").Code != 200 {
		t.Error("this Browser must stay signed in")
	}
	if got := other.Get("/"); got.Code != http.StatusSeeOther {
		t.Errorf("the other device: status %d, want signed out", got.Code)
	}
	if n := s.SessionCount(s.User); n != 1 {
		t.Errorf("%d sessions, want only the rotated one", n)
	}

	// The new password works and the old one does not.
	if w := s.SignedOut().PostPlain("/login", url.Values{"email": {s.User.Email}, "password": {newPassword}}); w.Code != http.StatusSeeOther {
		t.Errorf("new password: status %d", w.Code)
	}
	if w := s.SignedOut().PostPlain("/login", credentials(s.User, "")); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("old password: status %d", w.Code)
	}
}

func TestChangePasswordFormInAModalAndAsAPage(t *testing.T) {
	s := systemtest.New(t)

	w := s.Browser.GetHTMX("/password")
	if w.Code != 200 || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("htmx: status %d, body %q", w.Code, w.Body.String())
	}
	d := s.Page(w)
	if !strings.Contains(d.Text(), "Signed in as "+s.User.Email) || strings.TrimSpace(d.Find("h1").Text()) != "Change password" {
		t.Errorf("heading missing: %q", d.Text())
	}
	if d.Find("input[name=current_password][autocomplete=current-password]").Length() != 1 ||
		d.Find("input[name=new_password][autocomplete=new-password]").Length() != 1 ||
		d.Find("input[name=email][autocomplete=username]").AttrOr("value", "") != s.User.Email ||
		d.Find("input[type=password]").Length() != 2 || d.Find("input[type=checkbox]").Length() != 1 {
		t.Error("form fields wrong")
	}
	if !strings.Contains(d.Text(), "Changing your password signs you out on every other device.") {
		t.Error("note missing")
	}

	page := s.Page(s.Get("/password"))
	if page.Find("header.navbar").Length() != 1 || page.Find("input[name=new_password]").Length() != 1 {
		t.Error("the plain GET must be a full shell page with the form")
	}

	hdr := s.Page(s.Get("/"))
	if hdr.Find("header button[hx-get='/password']").Length() != 1 || hdr.Find("dialog.modal").Length() != 1 {
		t.Error("header email button / modal missing")
	}
	if got := s.SignedOut().Get("/password"); got.Code != http.StatusSeeOther {
		t.Errorf("signed out: status %d", got.Code)
	}
}

func TestWrongCurrentPasswordIsRefusedInline(t *testing.T) {
	s := systemtest.New(t)
	other := s.SignInAs(s.User)
	for name, post := range map[string]func(string, url.Values) *httptest.ResponseRecorder{
		"htmx": s.Browser.Post, "plain": s.Browser.PostPlain,
	} {
		w := post("/password", changeForm("not my password", newPassword))
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Current password is wrong.") {
			t.Errorf("%s: status %d, body %q", name, w.Code, w.Body.String())
		}
		if s.SessionCookie(w) != nil {
			t.Errorf("%s: no cookie change on failure", name)
		}
	}
	if other.Get("/").Code != 200 {
		t.Error("a refused change must not sign out other devices")
	}
	if w := s.SignedOut().PostPlain("/login", credentials(s.User, "")); w.Code != http.StatusSeeOther {
		t.Error("the password must be unchanged")
	}
}

func TestTooShortNewPasswordIsRefusedInline(t *testing.T) {
	s := systemtest.New(t)
	w := s.Browser.Post("/password", changeForm(testfixtures.Password, "short"))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Use at least 15 characters (this one has 5).") {
		t.Fatalf("status %d, body %q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<html") {
		t.Error("htmx failure must be a fragment")
	}
	if strings.Contains(w.Body.String(), `value="short"`) {
		t.Error("passwords must never be echoed")
	}
}
