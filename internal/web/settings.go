package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/ihorbryk/seo-site-checker/internal/project"
)

// settingsView is the Request settings form's values as text, so that a
// refused submission is shown back exactly as typed.
type settingsView struct {
	Schedule, UserAgent, TimeoutS, MaxParallel string
	RespectRobots                              bool
	Headers                                    []check.Header
	ExcludePatterns                            string
}

func viewOf(s project.RequestSettings) settingsView {
	return settingsView{
		Schedule: s.Schedule, UserAgent: s.UserAgent, TimeoutS: strconv.Itoa(s.TimeoutS), MaxParallel: strconv.Itoa(s.MaxParallel),
		RespectRobots: s.RespectRobots, Headers: s.Headers,
		ExcludePatterns: strings.Join(s.ExcludePatterns, "\n"),
	}
}

func viewOfForm(f url.Values) settingsView {
	v := settingsView{
		Schedule: f.Get("schedule"), UserAgent: f.Get("user_agent"), TimeoutS: f.Get("timeout_s"),
		MaxParallel: f.Get("max_parallel"), RespectRobots: f.Get("respect_robots") != "", ExcludePatterns: f.Get("exclude_patterns"),
	}
	names, values := f["header_name"], f["header_value"]
	for i := range max(len(names), len(values)) {
		var h check.Header
		if i < len(names) {
			h.Name = names[i]
		}
		if i < len(values) {
			h.Value = values[i]
		}
		v.Headers = append(v.Headers, h)
	}
	return v
}

// noiseView is the Noise rules form's values as text.
type noiseView struct{ HeaderNames, BodyMasks string }

func noiseViewOf(n project.NoiseRules) noiseView {
	return noiseView{HeaderNames: strings.Join(n.HeaderNames, "\n"), BodyMasks: strings.Join(n.BodyMasks, "\n")}
}

func (s *server) settingsScreen(w http.ResponseWriter, r *http.Request, p dbgen.Project, v settingsView, errs project.FieldErrors, saved bool) {
	s.settingsScreenN(w, r, p, v, errs, saved, nil, nil)
}

// settingsScreenN renders the Settings tab; nv is the Noise rules form's
// values (nil: the saved ones).
func (s *server) settingsScreenN(w http.ResponseWriter, r *http.Request, p dbgen.Project, v settingsView, errs project.FieldErrors, saved bool, nv *noiseView, nerrs project.FieldErrors) {
	if nv == nil {
		n, err := project.LoadNoiseRules(r.Context(), s.q, p.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		view := noiseViewOf(n)
		nv = &view
	}
	crumbs := projectCrumbs(p, crumb{Label: "Settings"})
	s.render(w, r, "settings", "Settings · "+p.Domain, crumbs, map[string]any{
		"Project": p, "Tab": "settings", "S": v, "Schedules": project.Schedules,
		"UserAgents": project.UserAgentSuggestions, "Errors": flat(errs), "Saved": saved,
		"N": *nv, "NoiseErrors": flatNoise(nerrs), "Builtin": diff.BuiltinRules(),
	})
}

func (s *server) settingsPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	rs, err := project.SettingsOf(p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.settingsScreen(w, r, p, viewOf(rs), nil, false)
}

func (s *server) saveSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	rs, errs := project.ParseRequestSettings(r.PostForm)
	if len(errs) > 0 {
		w.WriteHeader(http.StatusUnprocessableEntity)
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, "settingsResult", map[string]any{"Errors": flat(errs)})
			return
		}
		s.settingsScreen(w, r, p, viewOfForm(r.PostForm), errs, false)
		return
	}
	if err := project.SaveRequestSettings(r.Context(), s.q, id, rs); err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, "settingsResult", map[string]any{"Saved": true})
		return
	}
	http.Redirect(w, r, "/p/"+strconv.FormatInt(id, 10)+"/settings", http.StatusSeeOther)
}

// headerRow returns one blank custom header row for the "Add header" button.
func (s *server) headerRow(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.pathID(w, r); !ok {
		return
	}
	s.fragment(w, r, "headerRow", check.Header{})
}

func flat(errs project.FieldErrors) []string {
	var out []string
	for _, field := range []string{"schedule", "user_agent", "headers", "timeout_s", "max_parallel", "exclude_patterns"} {
		out = append(out, errs[field]...)
	}
	return out
}

func flatNoise(errs project.FieldErrors) []string {
	return append(append([]string{}, errs["header_names"]...), errs["body_masks"]...)
}

func (s *server) saveNoiseRules(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	n, errs := project.ParseNoiseRules(r.PostForm)
	if len(errs) > 0 {
		w.WriteHeader(http.StatusUnprocessableEntity)
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, "settingsResult", map[string]any{"Errors": flatNoise(errs)})
			return
		}
		rs, err := project.SettingsOf(p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		nv := noiseView{HeaderNames: r.PostForm.Get("header_names"), BodyMasks: r.PostForm.Get("body_masks")}
		s.settingsScreenN(w, r, p, viewOf(rs), nil, false, &nv, errs)
		return
	}
	if err := project.SaveNoiseRules(r.Context(), s.pool, id, n); err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, "settingsResult", map[string]any{"Saved": true})
		return
	}
	http.Redirect(w, r, "/p/"+strconv.FormatInt(id, 10)+"/settings", http.StatusSeeOther)
}
