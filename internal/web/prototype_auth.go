package web

// PROTOTYPE, throwaway. Wayfinder ticket "What do the auth screens and
// signed-in header look like?" (#52). Lives only on branch
// prototype/auth-screens; never merge.
//
// Three variants of the auth screens + signed-in header, switchable with
// ?variant=A|B|C (remembered in a cookie so it follows you onto real pages).
// Forms post to stubs that apply the decided rules; nothing touches the DB.
//
//   password "correct horse battery staple" signs in / is the current password
//   password "throttle" answers 429
//   email taken@example.com is already registered

import (
	"bytes"
	"html/template"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"unicode/utf8"
)

const protoGoodPassword = "correct horse battery staple"

var protoVariants = []struct{ Key, Name string }{
	{"A", "Standalone card"},
	{"B", "Inside the shell"},
	{"C", "Split page + modal"},
}

func protoVariant(w http.ResponseWriter, r *http.Request) string {
	valid := func(v string) bool { return v == "A" || v == "B" || v == "C" }
	if v := r.URL.Query().Get("variant"); valid(v) {
		http.SetCookie(w, &http.Cookie{Name: "proto_variant", Value: v, Path: "/"})
		return v
	}
	if c, err := r.Cookie("proto_variant"); err == nil && valid(c.Value) {
		return c.Value
	}
	return "A"
}

func protoData(w http.ResponseWriter, r *http.Request) map[string]any {
	v := protoVariant(w, r)
	name := ""
	for _, pv := range protoVariants {
		if pv.Key == v {
			name = pv.Name
		}
	}
	screen := ""
	if strings.HasPrefix(r.URL.Path, "/prototype/auth/") {
		screen = r.PathValue("screen")
	}
	return map[string]any{
		"V": v, "VName": name, "Variants": protoVariants,
		"UserEmail": "you@example.com",
		"SignedOut": screen == "login" || screen == "register",
		"Screen":    screen, "State": r.URL.Query().Get("state"),
		"States": map[string][]string{
			"login":    {"invalid", "throttled", "regoff", "expired", "loggedout"},
			"register": {"validation", "throttled"},
			"account":  {"validation", "throttled", "saved"},
		}[screen],
	}
}

func protoRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /prototype/auth/expired", s.protoExpired)
	mux.HandleFunc("POST /prototype/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/prototype/auth/login?state=loggedout", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /prototype/auth/{screen}", s.protoAuth)
	mux.HandleFunc("POST /prototype/auth/{screen}", s.protoAuthPost)
}

// protoExpired stands in for any signed-in route hit with a dead session.
func (s *server) protoExpired(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Path
	if cur := r.Header.Get("HX-Current-URL"); cur != "" {
		if u, err := url.Parse(cur); err == nil {
			next = u.RequestURI()
		}
	}
	login := "/prototype/auth/login?state=expired&next=" + url.QueryEscape(next)
	if r.Header.Get("HX-Request") == "true" {
		// Never swap a login page into a fragment: tell htmx to navigate.
		w.Header().Set("HX-Redirect", login)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, login, http.StatusSeeOther)
}

func protoScreenData(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	screen := r.PathValue("screen")
	if screen != "login" && screen != "register" && screen != "account" {
		return nil, false
	}
	p := protoData(w, r)
	errs := map[string]string{}
	d := map[string]any{
		"Screen": screen, "V": p["V"], "Proto": p, "Errors": errs,
		"Next": r.URL.Query().Get("next"), "Email": "",
		"Modal": r.URL.Query().Get("modal") == "1",
	}
	switch r.URL.Query().Get("state") {
	case "invalid":
		d["Email"] = "you@example.com"
		errs["_"] = "Wrong email or password."
	case "validation":
		if screen == "account" {
			errs["current"] = "Current password is wrong."
			errs["password"] = "Use at least 15 characters (this one has 9)."
		} else {
			d["Email"] = "you@example"
			errs["email"] = "Enter an email address like name@example.com."
			errs["password"] = "Use at least 15 characters (this one has 9)."
			errs["confirm"] = "The passwords don't match."
		}
	case "throttled":
		d["Email"] = "you@example.com"
		errs["_"] = "Too many attempts. Try again in 12 minutes."
	case "regoff":
		d["RegOff"] = true
	case "expired":
		d["Notice"] = "Your session has ended. Sign in again to continue."
		if d["Next"] == "" {
			d["Next"] = "/p/1/pages"
		}
	case "loggedout":
		d["Notice"] = "You've signed out."
	case "saved":
		d["Saved"] = true
	}
	return d, true
}

func (s *server) protoAuth(w http.ResponseWriter, r *http.Request) {
	d, ok := protoScreenData(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.protoRenderScreen(w, r, d, http.StatusOK)
}

func (s *server) protoRenderScreen(w http.ResponseWriter, r *http.Request, d map[string]any, status int) {
	screen, v := d["Screen"].(string), d["V"].(string)
	if screen == "account" {
		if d["Modal"].(bool) {
			s.fragment(w, r, "protoAccountModal", d)
			return
		}
		s.render(w, r, "protoAccountPage", "Change password", []crumb{{Label: "Change password"}}, d)
		return
	}
	title := map[string]string{"login": "Sign in", "register": "Create account"}[screen]
	if v == "B" {
		s.render(w, r, "protoAuthB", title, nil, d)
		return
	}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "protoAuth"+v, d); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = s.tmpl.ExecuteTemplate(w, "protoBare", map[string]any{
		"Title": title, "Content": template.HTML(buf.String()), "Proto": d["Proto"],
	})
}

func protoCheckNewPassword(pw, confirm string, hasConfirm bool, errs map[string]string) {
	if n := utf8.RuneCountInString(pw); n < 15 {
		errs["password"] = "Use at least 15 characters (this one has " + itoa(n) + ")."
	} else if len(pw) > 1024 {
		errs["password"] = "Use at most 1024 bytes."
	} else if hasConfirm && pw != confirm {
		errs["confirm"] = "The passwords don't match."
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (s *server) protoAuthPost(w http.ResponseWriter, r *http.Request) {
	d, ok := protoScreenData(w, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	errs := map[string]string{}
	d["Errors"] = errs
	delete(d, "Notice")
	screen := d["Screen"].(string)
	hasConfirm := d["V"] != "A"
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	pw := r.PostFormValue("password")
	d["Email"] = email
	d["Next"] = r.PostFormValue("next")
	status := http.StatusUnprocessableEntity
	if pw == "throttle" || r.PostFormValue("current") == "throttle" {
		errs["_"] = "Too many attempts. Try again in 12 minutes."
		w.Header().Set("Retry-After", "720")
		status = http.StatusTooManyRequests
	} else {
		switch screen {
		case "login":
			switch {
			case email == "" || pw == "":
				errs["_"] = "Enter your email and password."
			case pw != protoGoodPassword:
				errs["_"] = "Wrong email or password."
			default:
				next := r.PostFormValue("next")
				if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
					next = "/"
				}
				protoDone(w, r, next)
				return
			}
		case "register":
			a, err := mail.ParseAddress(email)
			at := strings.LastIndexByte(email, '@')
			switch {
			case err != nil || a.Name != "" || a.Address != email || at < 0 || !strings.Contains(email[at:], ".") || len(email) > 254:
				errs["email"] = "Enter an email address like name@example.com."
			case email == "taken@example.com":
				errs["email"] = "An account with this email already exists. Sign in instead."
			}
			protoCheckNewPassword(pw, r.PostFormValue("confirm"), hasConfirm, errs)
			if len(errs) == 0 {
				protoDone(w, r, "/")
				return
			}
		case "account":
			if r.PostFormValue("current") != protoGoodPassword {
				errs["current"] = "Current password is wrong."
			}
			protoCheckNewPassword(pw, r.PostFormValue("confirm"), hasConfirm, errs)
			if len(errs) == 0 {
				d["Saved"] = true
				status = http.StatusOK
			}
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = s.tmpl.ExecuteTemplate(w, "protoForm", d)
		return
	}
	s.protoRenderScreen(w, r, d, status)
}

func protoDone(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
