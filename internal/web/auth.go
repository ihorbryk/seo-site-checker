package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/jackc/pgx/v5"
)

// Cookie names. The __Host- prefix makes browsers require Secure, Path=/ and
// no Domain, so it is the default; INSECURE_COOKIES (plain-HTTP development)
// cannot use it.
const (
	secureCookieName   = "__Host-session"
	insecureCookieName = "session"
)

// CookieName is the session cookie's name for the given INSECURE_COOKIES setting.
func CookieName(insecure bool) string {
	if insecure {
		return insecureCookieName
	}
	return secureCookieName
}

func (s *server) cookieName() string { return CookieName(s.opts.InsecureCookies) }

// sessionToken returns the raw session token the request carries, or "".
func (s *server) sessionToken(r *http.Request) string {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

// sessionCookie is the session cookie carrying value for maxAge seconds.
func (s *server) sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: s.cookieName(), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: !s.opts.InsecureCookies, SameSite: http.SameSiteLaxMode,
	}
}

func (s *server) setSessionCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, s.sessionCookie(raw, int(session.MaxAge/time.Second)))
}

func (s *server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, s.sessionCookie("", -1))
}

// signedInUser resolves the request's session cookie to its User.
func (s *server) signedInUser(r *http.Request) (dbgen.User, bool, error) {
	raw := s.sessionToken(r)
	if raw == "" {
		return dbgen.User{}, false, nil
	}
	return session.Lookup(r.Context(), s.pool, raw, s.now())
}

type userKey struct{}

// currentUser is the signed-in User of a request that passed the signedIn
// gate. It panics on a public route: reading it there is a bug.
func currentUser(r *http.Request) dbgen.User {
	u, ok := r.Context().Value(userKey{}).(dbgen.User)
	if !ok {
		panic("web: currentUser on a request that did not pass the signedIn gate")
	}
	return u
}

func withUser(ctx context.Context, u dbgen.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// guard applies a route's access level.
func (s *server) guard(a access, next http.Handler) http.Handler {
	switch a.level {
	case publicLevel:
		return next
	case signedInLevel:
		return s.requireUser(next)
	case ownedLevel:
		return s.requireUser(s.requireOwner(a.kind, next))
	}
	panic("web: route with unknown access level")
}

func (s *server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok, err := s.signedInUser(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !ok {
			s.sendToLogin(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

// requireOwner answers 404 unless the {id} names a row of kind in a Project
// the signed-in User owns. It only gates: handlers still load their own rows.
func (s *server) requireOwner(kind ownerKind, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.pathID(w, r)
		if !ok {
			return
		}
		mine, err := s.owns(r.Context(), kind, id, currentUser(r).ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !mine {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ownerLookups finds the owning User's id of a row of each ownerKind. Every
// kind must have an entry: New refuses to start otherwise.
var ownerLookups = map[ownerKind]func(*dbgen.Queries, context.Context, int64) (int64, error){
	projectKind:  (*dbgen.Queries).ProjectOwner,
	pageKind:     (*dbgen.Queries).PageOwner,
	checkKind:    (*dbgen.Queries).CheckOwner,
	snapshotKind: (*dbgen.Queries).SnapshotOwner,
}

// owns reports whether the row of kind with id belongs to a Project owned by
// userID. A missing row is not owned.
func (s *server) owns(ctx context.Context, kind ownerKind, id, userID int64) (bool, error) {
	lookup, ok := ownerLookups[kind]
	if !ok {
		panic("web: unknown ownerKind")
	}
	owner, err := lookup(s.q, ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return owner == userID, err
}

// sendToLogin answers a signed-out request: a redirect to /login?next=<where
// they were>, or for htmx (which would swap the login page into a fragment)
// a 401 whose HX-Redirect sends the whole browser there.
func (s *server) sendToLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		target := "/login"
		if cur, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && cur.Path != "" {
			target += "?next=" + url.QueryEscape(cur.RequestURI())
		}
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

// localPath reports whether next is a same-site path: it starts with a single
// slash and holds no backslash or control character a browser could turn
// into another host.
func localPath(next string) bool {
	if len(next) == 0 || next[0] != '/' || (len(next) > 1 && (next[1] == '/' || next[1] == '\\')) {
		return false
	}
	for _, c := range next {
		if c == '\\' || c < ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

// hsts adds Strict-Transport-Security to every response.
func hsts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}
