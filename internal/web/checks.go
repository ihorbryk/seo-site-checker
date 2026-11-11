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

// pathID parses the {id} path value; ok is false (and a 404 written) when it
// is not a positive integer.
func (s *server) pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// notFoundOrFail writes a 404 for a missing row and a 500 for any other error.
func (s *server) notFoundOrFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	s.fail(w, r, err)
}

// sendTo sends the browser to href, through HX-Redirect for htmx requests.
func sendTo(w http.ResponseWriter, r *http.Request, href string) {
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
	Partial  bool

	// Report counts, shown once the Check has ended.
	Changed, Critical, SEO int64
	New, Vanished, Retired int64
	SeedFailed             bool
}

func (s *server) projectChecks(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
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
			Fetched: c.Fetched, Known: c.Known, Failures: c.Failures, Partial: c.State == check.StateCancelled,
			Changed: c.Changed, Critical: c.Critical, SEO: c.Seo,
			New: int64(c.CountNew), Vanished: int64(c.CountVanished), Retired: int64(c.CountRetired),
			SeedFailed: c.SeedFailed,
		})
	}
	s.render(w, r, "project", p.Domain, projectCrumbs(p, crumb{Label: "Checks"}), map[string]any{
		"Project": p, "Tab": "checks", "Checks": checks, "HasActive": active,
	})
}

func (s *server) runCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.q.GetProject(r.Context(), id); err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	c, err := check.Start(r.Context(), s.pool, s.river, id, check.TriggerManual)
	if err != nil && !errors.Is(err, check.ErrActive) {
		s.fail(w, r, err)
		return
	}
	// With a Check already active, show its progress instead of a second one.
	sendTo(w, r, fmt.Sprintf("/c/%d", c.ID))
}

// cancelCheck asks a queued or running Check to stop. Cancelling a Check that
// is not active does nothing.
func (s *server) cancelCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.q.GetCheck(r.Context(), id); err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	if _, err := s.q.RequestCancel(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	sendTo(w, r, fmt.Sprintf("/c/%d", id))
}

func (s *server) checkPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	c, err := s.q.GetCheck(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	p, err := s.q.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	crumbs := checkCrumbs(p, c.ID)
	title := fmt.Sprintf("Check #%d", c.ID)
	if check.Active(c.State) {
		prog, err := s.loadProgress(r, c)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.render(w, r, "checkRunning", title, crumbs, map[string]any{
			"Project": p, "Check": c, "Started": formatTime(c.StartedAt.Time, c.StartedAt.Valid), "Progress": prog,
		})
		return
	}
	data, err := s.buildReport(r, c, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "report", title, crumbs, data)
}

// progressFragment is the htmx fragment polled while a Check runs. Once the
// Check has ended it sends the browser to the full page, which is the Report.
func (s *server) progressFragment(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	c, err := s.q.GetCheck(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	if !check.Active(c.State) {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/c/%d", c.ID))
		return
	}
	prog, err := s.loadProgress(r, c)
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
	Discovered int64
	InFlight   []dbgen.ListInFlightFetchesRow
}

func (s *server) loadProgress(r *http.Request, c dbgen.Check) (progressView, error) {
	p, err := s.q.CheckProgress(r.Context(), c.ID)
	if err != nil {
		return progressView{}, err
	}
	inflight, err := s.q.ListInFlightFetches(r.Context(), c.ID)
	if err != nil {
		return progressView{}, err
	}
	discovered, err := s.q.CountNewlyDiscoveredPages(r.Context(), c.ID)
	if err != nil {
		return progressView{}, err
	}
	return progressView{
		CheckID: c.ID, Queued: c.State == check.StateQueued, Cancelling: c.State == check.StateCancelling,
		Finalizing: c.State == check.StateRunning && p.Fetched == p.Known,
		Fetched:    p.Fetched, Known: p.Known, Failures: p.Failures, Discovered: discovered, InFlight: inflight,
	}, nil
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
