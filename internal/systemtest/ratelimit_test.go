package systemtest_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

const tooManyMessage = "Too many attempts. Try again in"

func wantTooMany(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), tooManyMessage) {
		t.Fatalf("status %d, Retry-After %q, body %q; want 429 with the message", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
}

func wrongLogin(email string) url.Values {
	return url.Values{"email": {email}, "password": {"not the password"}}
}

func TestLoginAnswers429AfterTenFailuresForOneEmailEvenWithTheRightPassword(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()
	for i := 0; i < 10; i++ {
		if w := visitor.PostPlain("/login", wrongLogin(s.User.Email)); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("failure %d: status %d", i+1, w.Code)
		}
	}
	w := visitor.PostPlain("/login", credentials(s.User, ""))
	wantTooMany(t, w)
	if !strings.Contains(w.Body.String(), "<html") || s.SessionCookie(w) != nil {
		t.Error("a plain post gets the full page and no session")
	}
	w = visitor.Post("/login", credentials(s.User, ""))
	wantTooMany(t, w)
	if strings.Contains(w.Body.String(), "<html") {
		t.Error("htmx gets the form fragment")
	}
}

func TestLoginCountsFailuresForUnknownEmailsToo(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()
	for i := 0; i < 10; i++ {
		visitor.PostPlain("/login", wrongLogin("nobody@example.test"))
	}
	w := visitor.PostPlain("/login", wrongLogin("NOBODY@example.test "))
	wantTooMany(t, w)
}

func TestASuccessfulLoginClearsTheEmailCounterButNotTheIPs(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()
	for i := 0; i < 9; i++ {
		visitor.PostPlain("/login", wrongLogin(s.User.Email))
	}
	if w := visitor.PostPlain("/login", credentials(s.User, "")); w.Code != http.StatusSeeOther {
		t.Fatalf("success: status %d", w.Code)
	}
	// Nine more failures would trip the email limit had the success not cleared it.
	for i := 0; i < 9; i++ {
		if w := visitor.PostPlain("/login", wrongLogin(s.User.Email)); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("failure %d after the success: status %d", i+1, w.Code)
		}
	}
	// 18 failures so far from this IP: two more reach its limit of 20.
	for i := 0; i < 2; i++ {
		visitor.PostPlain("/login", wrongLogin(fmt.Sprintf("other-%d@example.test", i)))
	}
	w := visitor.PostPlain("/login", wrongLogin("fresh@example.test"))
	wantTooMany(t, w)
}

func TestLoginLimitsTheIPAcrossEmails(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()
	for i := 0; i < 20; i++ {
		visitor.PostPlain("/login", wrongLogin(fmt.Sprintf("someone-%d@example.test", i)))
	}
	w := visitor.PostPlain("/login", credentials(s.User, ""))
	wantTooMany(t, w)
}

func TestTheTrustedProxyHeaderSeparatesClients(t *testing.T) {
	s := systemtest.New(t, systemtest.TrustedProxyHeader("X-Forwarded-For"))
	visitor := s.SignedOut()
	from := func(ip string) http.Header { return http.Header{"X-Forwarded-For": {"6.6.6.6, " + ip}} }
	for i := 0; i < 20; i++ {
		visitor.Request("POST", "/login", wrongLogin(fmt.Sprintf("someone-%d@example.test", i)), from("198.51.100.1"))
	}
	if w := visitor.Request("POST", "/login", wrongLogin("a@example.test"), from("198.51.100.1")); w.Code != http.StatusTooManyRequests {
		t.Errorf("tripped client: status %d", w.Code)
	}
	if w := visitor.Request("POST", "/login", wrongLogin("b@example.test"), from("198.51.100.2")); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("another client: status %d, want it unaffected", w.Code)
	}
}

func TestRegisterAnswers429AfterFiveAttemptsPerIPSuccessesIncluded(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()
	for i := 0; i < 5; i++ {
		w := visitor.PostPlain("/register", registration(fmt.Sprintf("new-%d@example.test", i), testfixtures.Password))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("registration %d: status %d", i+1, w.Code)
		}
	}
	w := visitor.PostPlain("/register", registration("one-more@example.test", testfixtures.Password))
	wantTooMany(t, w)
	if !strings.Contains(w.Body.String(), "<html") {
		t.Error("a plain post gets the full page")
	}
	w = visitor.Post("/register", registration("one-more@example.test", testfixtures.Password))
	wantTooMany(t, w)
	if strings.Contains(w.Body.String(), "<html") {
		t.Error("htmx gets the form fragment")
	}
}

func TestChangePasswordAnswers429AfterTenWrongCurrentPasswords(t *testing.T) {
	s := systemtest.New(t)
	for i := 0; i < 10; i++ {
		if w := s.Browser.Post("/password", changeForm("wrong", newPassword)); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("failure %d: status %d", i+1, w.Code)
		}
	}
	w := s.Browser.Post("/password", changeForm(testfixtures.Password, newPassword))
	wantTooMany(t, w)
	w = s.Browser.PostPlain("/password", changeForm(testfixtures.Password, newPassword))
	wantTooMany(t, w)
	if !strings.Contains(w.Body.String(), "<html") {
		t.Error("a plain post gets the full page")
	}
	// Another User is unaffected.
	other := s.NewUser()
	if w := other.Post("/password", changeForm("wrong", newPassword)); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("another User: status %d", w.Code)
	}
}

func TestAParallelBurstOfFailuresCannotExceedTheEmailLimit(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()
	var wrong, limited atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch visitor.PostPlain("/login", wrongLogin(s.User.Email)).Code {
			case http.StatusUnprocessableEntity:
				wrong.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
			}
		}()
	}
	wg.Wait()
	if wrong.Load() != 10 || limited.Load() != 30 {
		t.Errorf("%d attempts checked, %d refused; want exactly 10 and 30", wrong.Load(), limited.Load())
	}
}

func TestAHugeEmailIsLimitedLikeAnyOther(t *testing.T) {
	s := systemtest.New(t)
	visitor := s.SignedOut()
	huge := strings.Repeat("a", 60000) + "@example.test"
	for i := 0; i < 10; i++ {
		visitor.PostPlain("/login", wrongLogin(huge))
	}
	wantTooMany(t, visitor.PostPlain("/login", wrongLogin(huge)))
}

func TestOneWarnLinePerRateLimitTrip(t *testing.T) {
	s := systemtest.New(t, systemtest.WebLog())
	visitor := s.SignedOut()
	for i := 0; i < 10; i++ {
		visitor.PostPlain("/login", wrongLogin(s.User.Email))
	}
	for i := 0; i < 5; i++ {
		wantTooMany(t, visitor.PostPlain("/login", wrongLogin(s.User.Email)))
	}
	var warns []string
	for _, l := range s.WebLogLines() {
		if strings.Contains(l, "level=WARN") {
			warns = append(warns, l)
		}
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "login-email") || strings.Contains(warns[0], s.User.Email) {
		t.Errorf("warn lines %q; want one for login-email without the raw email", warns)
	}
}
