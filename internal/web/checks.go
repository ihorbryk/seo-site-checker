package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// idParam parses the {id} path value; ok is false (and a 404 written) when it
// is not a positive integer.
func (s *server) idParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// notFoundOr writes a 404 for a missing row and a 500 for any other error.
func (s *server) notFoundOr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	s.fail(w, r, err)
}

// redirect sends the browser to href, through HX-Redirect for htmx requests.
func redirect(w http.ResponseWriter, r *http.Request, href string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", href)
		return
	}
	http.Redirect(w, r, href, http.StatusSeeOther)
}

type checkRow struct {
	ID       int64
	Trigger  string
	State    string
	Active   bool
	Started  string
	Duration string
	Fetched  int64
	Known    int64
	Failures int64
}

func (s *server) projectChecks(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idParam(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	rows, err := s.q.ListCheckRows(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var checks []checkRow
	active := false
	for _, c := range rows {
		active = active || check.Active(c.State)
		checks = append(checks, checkRow{
			ID: c.ID, Trigger: c.Trigger, State: c.State, Active: check.Active(c.State),
			Started: formatTime(c.StartedAt.Time, c.StartedAt.Valid), Duration: duration(c.StartedAt, c.FinishedAt),
			Fetched: c.Fetched, Known: c.Known, Failures: c.Failures,
		})
	}
	s.render(w, r, "project", p.Domain, projectCrumbs(p, false), map[string]any{
		"Project": p, "Checks": checks, "HasActive": active,
	})
}

func (s *server) projectPages(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idParam(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	pages, err := s.q.ListPagePool(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "pages", p.Domain, projectCrumbs(p, false), map[string]any{"Project": p, "Pages": pages})
}

func (s *server) runCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idParam(w, r)
	if !ok {
		return
	}
	if _, err := s.q.GetProject(r.Context(), id); err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	c, err := check.Start(r.Context(), s.pool, s.river, id, check.TriggerManual)
	if err != nil && !errors.Is(err, check.ErrActive) {
		s.fail(w, r, err)
		return
	}
	// With a Check already active, show its progress instead of a second one.
	redirect(w, r, fmt.Sprintf("/c/%d", c.ID))
}

func (s *server) checkPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idParam(w, r)
	if !ok {
		return
	}
	c, err := s.q.GetCheck(r.Context(), id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	p, err := s.q.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	crumbs := append(projectCrumbs(p, true), crumb{Label: fmt.Sprintf("Check #%d", c.ID)})
	data := map[string]any{
		"Project": p, "Check": c,
		"Started": formatTime(c.StartedAt.Time, c.StartedAt.Valid), "Finished": formatTime(c.FinishedAt.Time, c.FinishedAt.Valid),
		"Duration": duration(c.StartedAt, c.FinishedAt),
	}
	if check.Active(c.State) {
		prog, err := s.progress(r, c)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		data["Progress"] = prog
		s.render(w, r, "checkRunning", fmt.Sprintf("Check #%d", c.ID), crumbs, data)
		return
	}
	failures, err := s.q.ListFetchFailures(r.Context(), c.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data["Failures"] = failures
	disc, err := s.q.CheckDiscovery(r.Context(), c.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data["NewPages"], data["Dropped"] = disc.NewPages, disc.Dropped
	s.render(w, r, "report", fmt.Sprintf("Check #%d", c.ID), crumbs, data)
}

// checkProgress is the htmx fragment polled while a Check runs. Once the
// Check has ended it sends the browser to the full page, which is the Report.
func (s *server) checkProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idParam(w, r)
	if !ok {
		return
	}
	c, err := s.q.GetCheck(r.Context(), id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	if !check.Active(c.State) {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/c/%d", c.ID))
		return
	}
	prog, err := s.progress(r, c)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.fragment(w, r, "progress", prog)
}

type progressView struct {
	CheckID    int64
	Queued     bool
	Finalizing bool
	Cancelling bool
	Fetched    int64
	Known      int64
	Failures   int64
	InFlight   []dbgen.ListInFlightFetchesRow
}

func (s *server) progress(r *http.Request, c dbgen.Check) (progressView, error) {
	p, err := s.q.CheckProgress(r.Context(), c.ID)
	if err != nil {
		return progressView{}, err
	}
	inflight, err := s.q.ListInFlightFetches(r.Context(), c.ID)
	if err != nil {
		return progressView{}, err
	}
	return progressView{
		CheckID: c.ID, Queued: c.State == check.StateQueued, Cancelling: c.State == check.StateCancelling,
		Finalizing: c.State == check.StateRunning && p.Fetched == p.Known,
		Fetched:    p.Fetched, Known: p.Known, Failures: p.Failures, InFlight: inflight,
	}, nil
}

func projectCrumbs(p dbgen.Project, link bool) []crumb {
	cs := []crumb{{Label: "Projects", Href: "/"}}
	if link {
		return append(cs, crumb{Label: p.Domain, Href: fmt.Sprintf("/p/%d", p.ID)})
	}
	return append(cs, crumb{Label: p.Domain})
}

func formatTime(t time.Time, valid bool) string {
	if !valid {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05") + " UTC"
}

func duration(start, end pgtype.Timestamptz) string {
	if !start.Valid || !end.Valid {
		return "—"
	}
	return end.Time.Sub(start.Time).Round(time.Millisecond).String()
}
