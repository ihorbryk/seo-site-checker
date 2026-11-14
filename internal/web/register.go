package web

import (
	"errors"
	"net/http"

	"github.com/ihorbryk/seo-site-checker/internal/user"
)

const errEmailTaken = "An account with this email already exists. Sign in instead."

func (s *server) registerScreen(w http.ResponseWriter, r *http.Request, status int, email, emailErr, passwordErr string) {
	s.authPage(w, r, status, "Create account", "Create your account", "registerForm", map[string]any{
		"Email": email, "EmailError": emailErr, "PasswordError": passwordErr,
	})
}

func (s *server) registerPage(w http.ResponseWriter, r *http.Request) {
	s.registerScreen(w, r, http.StatusOK, "", "", "")
}

// register creates a User and signs them in: validate, create, start a session.
func (s *server) register(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")

	var emailErr, passwordErr string
	norm, err := user.NormalizeEmail(email)
	if err != nil {
		emailErr = err.Error()
	}
	if err := user.ValidatePassword(password); err != nil {
		passwordErr = err.Error()
	}
	if emailErr != "" || passwordErr != "" {
		s.registerScreen(w, r, http.StatusUnprocessableEntity, email, emailErr, passwordErr)
		return
	}

	u, err := user.Create(r.Context(), s.pool, s.hasher, norm, password)
	switch {
	case errors.Is(err, user.ErrEmailTaken):
		s.registerScreen(w, r, http.StatusUnprocessableEntity, norm, errEmailTaken, "")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	sendTo(w, r, "/")
}
