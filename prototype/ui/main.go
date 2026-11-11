// PROTOTYPE — throwaway. Answers wayfinder ticket "What do the UI screens look like?".
//
// Plan: three structurally different navigation models for the same screens,
// switchable via ?variant=A|B|C (floating bar at the bottom, ← → keys), plus a
// Diff mode toggle (?diff=split|unified). All data is fake and in memory.
//
//	A  Pages & tabs     — classic multi-page app: breadcrumbs, Project tabs, every screen is its own URL.
//	B  Sidebar + pane   — persistent Project sidebar; Report and Page pool are master/detail,
//	                      clicking a row loads the Diff / Page history into a right pane via htmx.
//	C  Changes feed     — home is a cross-Project feed of Checks; a Project is a timeline;
//	                      Diffs expand inline instead of navigating.
//
// Run: go run ./prototype/ui   (then open http://localhost:8099)
package main

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed templates.html
var tmplFS embed.FS

var variants = []struct{ Key, Name string }{
	{"A", "Pages & tabs"},
	{"B", "Sidebar + pane"},
	{"C", "Changes feed"},
}

type app struct {
	db   *DB
	tmpl *template.Template
}

type crumb struct{ Label, Href string }

func main() {
	a := &app{db: seed()}
	a.tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"ago":  ago,
		"dict": dict,
		"add":  func(a, b int) int { return a + b },
		"join": strings.Join,
		"pct": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a * 100 / b
		},
		"statusClass": func(s int) string { return fmt.Sprintf("s%dxx", s/100) },
		"variants":    func() any { return variants },
		"clock":       func(t time.Time) string { return t.Format("Jan 2 15:04") },
	}).ParseFS(tmplFS, "templates.html"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("POST /projects", a.stub("Project created (prototype: not persisted). Its first Check would be enqueued now."))
	mux.HandleFunc("GET /p/{id}", a.project)
	mux.HandleFunc("GET /p/{id}/checks", a.checks)
	mux.HandleFunc("GET /p/{id}/pages", a.pages)
	mux.HandleFunc("GET /p/{id}/settings", a.settings)
	mux.HandleFunc("POST /p/{id}/settings", a.stub("Saved (prototype: not persisted). Applies from the next Check."))
	mux.HandleFunc("POST /p/{id}/noise", a.saveNoise)
	mux.HandleFunc("POST /p/{id}/run", a.stub("Check enqueued (prototype: nothing runs)."))
	mux.HandleFunc("GET /c/{id}", a.check)
	mux.HandleFunc("GET /c/{id}/progress", a.progress)
	mux.HandleFunc("POST /c/{id}/cancel", a.stub("Cancelling… the Check will end with a partial Report (prototype: nothing happens)."))
	mux.HandleFunc("GET /pg/{id}", a.page)
	mux.HandleFunc("POST /pg/{id}/toggle", a.toggleIgnore)
	mux.HandleFunc("GET /s/{id}", a.snapshot)
	mux.HandleFunc("GET /d/{id}", a.diff)

	log.Println("PROTOTYPE UI on http://localhost:8099  (?variant=A|B|C, ?diff=split|unified)")
	log.Fatal(http.ListenAndServe(":8099", mux))
}

// ---------- plumbing ----------

func pref(w http.ResponseWriter, r *http.Request, name, def string, allowed ...string) string {
	if v := r.URL.Query().Get(name); v != "" {
		for _, x := range allowed {
			if x == v {
				http.SetCookie(w, &http.Cookie{Name: name, Value: v, Path: "/"})
				return v
			}
		}
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return def
}

func (a *app) render(w http.ResponseWriter, r *http.Request, screen, title string, cur *Project, crumbs []crumb, data map[string]any) {
	v := pref(w, r, "variant", "A", "A", "B", "C")
	if data == nil {
		data = map[string]any{}
	}
	data["V"] = v
	data["DiffMode"] = pref(w, r, "diff", "split", "split", "unified")
	data["Cur"] = cur
	name := v + "/" + screen
	if a.tmpl.Lookup(name) == nil {
		name = screen
	}
	var buf bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Write(buf.Bytes())
		return
	}
	curName := ""
	for _, x := range variants {
		if x.Key == v {
			curName = x.Name
		}
	}
	err := a.tmpl.ExecuteTemplate(w, v+"/shell", map[string]any{
		"Title": title, "Content": template.HTML(buf.String()), "Projects": a.db.Projects,
		"Cur": cur, "Crumbs": crumbs, "V": v, "VName": curName, "Screen": screen,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (a *app) stub(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<div class="flash">%s</div>`, template.HTMLEscapeString(msg))
	}
}

func idOf(r *http.Request) int { n, _ := strconv.Atoi(r.PathValue("id")); return n }

func (a *app) proj(r *http.Request) *Project {
	n := idOf(r)
	if n < 1 || n > len(a.db.Projects) {
		return nil
	}
	return a.db.Projects[n-1]
}

func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func projCrumbs(p *Project, extra ...crumb) []crumb {
	return append([]crumb{{"Projects", "/"}, {p.Domain, fmt.Sprintf("/p/%d", p.ID)}}, extra...)
}

// ---------- handlers ----------

func (a *app) home(w http.ResponseWriter, r *http.Request) {
	var feed []*Check
	for _, p := range a.db.Projects {
		feed = append(feed, p.Checks...)
	}
	// newest first (running first)
	for i := 0; i < len(feed); i++ {
		for j := i + 1; j < len(feed); j++ {
			if feed[j].Started.After(feed[i].Started) {
				feed[i], feed[j] = feed[j], feed[i]
			}
		}
	}
	a.render(w, r, "home", "Projects", nil, nil, map[string]any{"Projects": a.db.Projects, "Feed": feed})
}

func (a *app) project(w http.ResponseWriter, r *http.Request) {
	p := a.proj(r)
	if p == nil {
		http.NotFound(w, r)
		return
	}
	v := pref(w, r, "variant", "A", "A", "B", "C")
	if v == "B" {
		http.Redirect(w, r, fmt.Sprintf("/c/%d", p.Latest().ID), http.StatusFound)
		return
	}
	if v == "A" {
		a.checks(w, r)
		return
	}
	a.render(w, r, "project", p.Domain, p, projCrumbs(p), map[string]any{"P": p})
}

func (a *app) checks(w http.ResponseWriter, r *http.Request) {
	p := a.proj(r)
	a.render(w, r, "checks", p.Domain+" · Checks", p, projCrumbs(p, crumb{"Checks", ""}), map[string]any{"P": p, "Tab": "checks"})
}

func (a *app) pages(w http.ResponseWriter, r *http.Request) {
	p := a.proj(r)
	st := r.URL.Query().Get("state")
	a.render(w, r, "pages", p.Domain+" · Page pool", p, projCrumbs(p, crumb{"Page pool", ""}),
		map[string]any{"P": p, "Tab": "pages", "State": st, "List": p.PagesIn(st),
			"States": []string{"active", "orphaned", "retired", "ignored", "excluded"}})
}

func (a *app) settings(w http.ResponseWriter, r *http.Request) {
	p := a.proj(r)
	a.render(w, r, "settings", p.Domain+" · Settings", p, projCrumbs(p, crumb{"Settings", ""}),
		map[string]any{"P": p, "Tab": "settings", "BuiltinHeaders": builtinNoiseHeaders})
}

func (a *app) saveNoise(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var errs []string
	for i, line := range strings.Split(r.FormValue("masks"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := regexp.Compile(line); err != nil {
			errs = append(errs, fmt.Sprintf("line %d: %v", i+1, err))
		}
	}
	if len(errs) > 0 {
		fmt.Fprintf(w, `<div class="flash err">Not saved — invalid RE2:<br>%s</div>`, template.HTMLEscapeString(strings.Join(errs, "\n")))
		return
	}
	fmt.Fprint(w, `<div class="flash">Noise rules saved (prototype: not persisted). Applies from the next Check.</div>`)
}

func (a *app) check(w http.ResponseWriter, r *http.Request) {
	c := a.db.Checks[idOf(r)]
	if c == nil {
		http.NotFound(w, r)
		return
	}
	p := c.Project
	title := fmt.Sprintf("Check #%d", c.ID)
	data := map[string]any{"P": p, "C": c}
	if c.Status != "running" {
		data["R"] = c.Report()
	} else {
		data["Prog"] = c.Progress()
	}
	a.render(w, r, "check", p.Domain+" · "+title, p, projCrumbs(p, crumb{"Checks", fmt.Sprintf("/p/%d/checks", p.ID)}, crumb{title, ""}), data)
}

func (a *app) progress(w http.ResponseWriter, r *http.Request) {
	c := a.db.Checks[idOf(r)]
	a.tmpl.ExecuteTemplate(w, "progress", c.Progress())
}

func (a *app) page(w http.ResponseWriter, r *http.Request) {
	pg := a.db.Pages[idOf(r)]
	if pg == nil {
		http.NotFound(w, r)
		return
	}
	p := pg.Project
	a.render(w, r, "page", pg.Path, p, projCrumbs(p, crumb{"Page pool", fmt.Sprintf("/p/%d/pages", p.ID)}, crumb{pg.Path, ""}),
		map[string]any{"P": p, "Pg": pg})
}

func (a *app) toggleIgnore(w http.ResponseWriter, r *http.Request) {
	pg := a.db.Pages[idOf(r)]
	if pg.State == "ignored" {
		pg.State = "active"
	} else {
		pg.State = "ignored"
	}
	a.tmpl.ExecuteTemplate(w, "ignoreBtn", pg)
}

func (a *app) snapshot(w http.ResponseWriter, r *http.Request) {
	s := a.db.Snapshots[idOf(r)]
	if s == nil {
		http.NotFound(w, r)
		return
	}
	p := s.Page.Project
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "seo"
	}
	var next *Snapshot
	for i, x := range s.Page.Snapshots {
		if x == s && i+1 < len(s.Page.Snapshots) {
			next = s.Page.Snapshots[i+1]
		}
	}
	a.render(w, r, "snapshot", fmt.Sprintf("Snapshot #%d", s.ID), p,
		projCrumbs(p, crumb{s.Page.Path, fmt.Sprintf("/pg/%d", s.Page.ID)}, crumb{fmt.Sprintf("Snapshot #%d", s.ID), ""}),
		map[string]any{"P": p, "S": s, "Tab": tab, "Next": next, "Lines": strings.Split(s.Body, "\n")})
}

func (a *app) diff(w http.ResponseWriter, r *http.Request) {
	s := a.db.Snapshots[idOf(r)]
	if s == nil {
		http.NotFound(w, r)
		return
	}
	p := s.Page.Project
	a.render(w, r, "diff", "Diff · "+s.Page.Path, p,
		projCrumbs(p, crumb{fmt.Sprintf("Check #%d", s.Check.ID), fmt.Sprintf("/c/%d", s.Check.ID)}, crumb{"Diff " + s.Page.Path, ""}),
		map[string]any{"P": p, "D": BuildDiff(s)})
}
