package web

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/ihorbryk/seo-site-checker/internal/user"
)

const (
	errWrongCurrentPassword = "Current password is wrong."
	noticePasswordChanged   = "Password changed. You're signed out on every other device."
)

// passwordScreen writes the change-password form: the modal panel alone for
// htmx, else the same panel on a full shell page.
func (s *server) passwordScreen(w http.ResponseWriter, r *http.Request, status int, errMsg, done string) {
	u := currentUser(r)
	htmx := r.Header.Get("HX-Request") == "true"
	data := map[string]any{"Email": u.Email, "Error": errMsg, "Done": done, "Modal": htmx}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "passwordPanel", data); err != nil {
		s.fail(w, r, err)
		return
	}
	if htmx {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = buf.WriteTo(w)
		return
	}
	data["Panel"] = template.HTML(buf.String())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	s.render(w, r, "passwordPage", "Change password", []crumb{{Label: "Change password"}}, data)
}

func (s *server) passwordPage(w http.ResponseWriter, r *http.Request) {
	s.passwordScreen(w, r, http.StatusOK, "", "")
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	u := currentUser(r)
	current, next := r.PostFormValue("current_password"), r.PostFormValue("new_password")

	// Charged up front as if the current password is wrong (see login); a
	// correct one gives the hit back.
	key := strconv.FormatInt(u.ID, 10)
	if retry, ok := s.reserve(limitCheck{l: s.limits.passwordUser, key: key}); !ok {
		s.passwordScreen(w, r, http.StatusTooManyRequests, tooMany(w, retry), "")
		return
	}
	ok := false
	if len(current) <= user.MaxPasswordBytes {
		ok, _ = s.hasher.Verify(current, u.PasswordHash)
	}
	if !ok {
		s.passwordScreen(w, r, http.StatusUnprocessableEntity, errWrongCurrentPassword, "")
		return
	}
	s.limits.passwordUser.Refund(key)
	if err := user.ValidatePassword(next); err != nil {
		s.passwordScreen(w, r, http.StatusUnprocessableEntity, err.Error(), "")
		return
	}
	hash, err := s.hasher.Hash(next)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.replacePassword(w, r, u.ID, hash); err != nil {
		s.fail(w, r, err)
		return
	}
	s.passwordScreen(w, r, http.StatusOK, "", noticePasswordChanged)
}

// replacePassword stores the new hash, signs the User out everywhere and
// gives this browser a fresh session, all in one transaction.
func (s *server) replacePassword(w http.ResponseWriter, r *http.Request, userID int64, hash string) error {
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := dbgen.New(tx)
	if err := q.SetUserPasswordHash(ctx, dbgen.SetUserPasswordHashParams{ID: userID, PasswordHash: hash}); err != nil {
		return err
	}
	if _, err := q.DeleteSessionsByUser(ctx, userID); err != nil {
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
