package web

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/ihorbryk/seo-site-checker/internal/user"
	"github.com/jackc/pgx/v5"
)

const (
	noticeSignInToContinue = "Sign in to continue."
	noticeSignedOut        = "You've signed out."
	errWrongCredentials    = "Wrong email or password."
	errMissingCredentials  = "Enter your email and password."
)

// authPage writes a sign-in/registration screen: the form alone for htmx
// (which swaps the form itself), else a standalone document in the split
// layout. formTmpl names the form template.
func (s *server) authPage(w http.ResponseWriter, r *http.Request, status int, title, heading, formTmpl string, data any) {
	var form bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&form, formTmpl, data); err != nil {
		s.fail(w, r, err)
		return
	}
	out := form.Bytes()
	if r.Header.Get("HX-Request") != "true" {
		var page bytes.Buffer
		// A form may have a "<form>Below" template: the switch link under it.
		var below bytes.Buffer
		if t := s.tmpl.Lookup(formTmpl + "Below"); t != nil {
			if err := t.Execute(&below, data); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		err := s.tmpl.ExecuteTemplate(&page, "authDoc", map[string]any{
			"Title": title, "Heading": heading, "Form": template.HTML(form.String()),
			"Below": template.HTML(below.String()),
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out = page.Bytes()
	}
	writeHTML(w, status, out)
}

// writeHTML sends body as an HTML response with the given status.
func writeHTML(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// loginView is what the sign-in form template shows. Callers set Email, Next
// and Error; loginScreen fills in the rest.
type loginView struct {
	Email, Next, Error string
	Notice             string
	CanRegister        bool
}

func (s *server) loginScreen(w http.ResponseWriter, r *http.Request, status int, v loginView) {
	if !localPath(v.Next) {
		v.Next = ""
	}
	switch {
	case r.URL.Query().Get("signedout") == "1":
		v.Notice = noticeSignedOut
	case v.Next != "":
		v.Notice = noticeSignInToContinue
	}
	v.CanRegister = !s.opts.DenyRegistration
	s.authPage(w, r, status, "Sign in", "Sign in", "loginForm", v)
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.loginScreen(w, r, http.StatusOK, loginView{Next: r.URL.Query().Get("next")})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	email := user.CanonicalEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	next := r.PostFormValue("next")
	refuse := func(msg string) {
		s.loginScreen(w, r, http.StatusUnprocessableEntity, loginView{Email: email, Next: next, Error: msg})
	}

	if email == "" || password == "" {
		refuse(errMissingCredentials)
		return
	}
	// Charge both limiters now as if this attempt fails: concurrent requests
	// then cannot all pass the check before any failure is recorded. A
	// success gives the hit back below.
	ip := limitCheck{l: s.limits.loginIP, key: s.clientIP(r)}
	em := limitCheck{l: s.limits.loginEmail, key: emailKey(email)}
	if retry, ok := s.reserve(ip, em); !ok {
		s.loginScreen(w, r, http.StatusTooManyRequests, loginView{Email: email, Next: next, Error: tooMany(w, retry)})
		return
	}
	u, err := s.q.GetUserByEmail(r.Context(), email)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, err)
		return
	}
	// Unknown emails verify against a dummy hash so timing does not reveal
	// which emails have accounts. Over-long passwords never reach argon2.
	hash := s.dummyHash
	if found {
		hash = u.PasswordHash
	}
	ok, needsRehash := false, false
	if len(password) <= user.MaxPasswordBytes {
		ok, needsRehash = s.hasher.Verify(password, hash)
	}
	if !found || !ok {
		refuse(errWrongCredentials)
		return
	}
	s.limits.loginIP.Refund(ip.key)
	s.limits.loginEmail.Clear(em.key)
	if needsRehash {
		s.upgradeHash(r, u.ID, password)
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	if !localPath(next) {
		next = "/"
	}
	sendTo(w, r, next)
}

// upgradeHash replaces a hash made with outdated parameters. It is best
// effort: the User is signed in either way.
func (s *server) upgradeHash(r *http.Request, userID int64, password string) {
	hash, err := s.hasher.Hash(password)
	if err == nil {
		err = s.q.SetUserPasswordHash(r.Context(), dbgen.SetUserPasswordHashParams{ID: userID, PasswordHash: hash})
	}
	if err != nil {
		s.log.Error("rehash password", "err", err)
	}
}

// startSession signs the browser in as userID: it always creates a new
// session and deletes any the browser held, so a planted token never becomes
// a signed-in one.
func (s *server) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	return s.startSessionAfter(w, r, userID, func(ctx context.Context, tx pgx.Tx) error {
		if old := s.sessionToken(r); old != "" {
			return session.Delete(ctx, tx, old)
		}
		return nil
	})
}

// startSessionAfter runs prepare, then creates userID's new session and sets
// its cookie, all in one transaction.
func (s *server) startSessionAfter(w http.ResponseWriter, r *http.Request, userID int64, prepare func(ctx context.Context, tx pgx.Tx) error) error {
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := prepare(ctx, tx); err != nil {
		return err
	}
	raw, err := session.Create(ctx, tx, userID, s.now())
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.setSessionCookie(w, raw)
	return nil
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if raw := s.sessionToken(r); raw != "" {
		if err := session.Delete(r.Context(), s.pool, raw); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.clearSessionCookie(w)
	sendTo(w, r, "/login?signedout=1")
}
