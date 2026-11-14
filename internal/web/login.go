package web

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"strings"

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
func (s *server) authPage(w http.ResponseWriter, r *http.Request, status int, title, heading, formTmpl string, data map[string]any) {
	var form bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&form, formTmpl, data); err != nil {
		s.fail(w, r, err)
		return
	}
	out := form.Bytes()
	if r.Header.Get("HX-Request") != "true" {
		var page bytes.Buffer
		err := s.tmpl.ExecuteTemplate(&page, "authDoc", map[string]any{
			"Title": title, "Heading": heading, "Form": template.HTML(form.String()),
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out = page.Bytes()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out)
}

func (s *server) loginScreen(w http.ResponseWriter, r *http.Request, status int, email, next, errMsg string) {
	if !localPath(next) {
		next = ""
	}
	notice := ""
	switch {
	case r.URL.Query().Get("signedout") == "1":
		notice = noticeSignedOut
	case next != "":
		notice = noticeSignInToContinue
	}
	s.authPage(w, r, status, "Sign in", "Sign in", "loginForm", map[string]any{
		"Email": email, "Next": next, "Notice": notice, "Error": errMsg,
	})
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.loginScreen(w, r, http.StatusOK, "", r.URL.Query().Get("next"), "")
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	password := r.PostFormValue("password")
	next := r.PostFormValue("next")
	refuse := func(msg string) { s.loginScreen(w, r, http.StatusUnprocessableEntity, email, next, msg) }

	if email == "" || password == "" {
		refuse(errMissingCredentials)
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
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if old := s.sessionToken(r); old != "" {
		if err := session.Delete(ctx, tx, old); err != nil {
			return err
		}
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
