package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/redirect"
	"github.com/ihorbryk/seo-site-checker/internal/seo"
	"github.com/jackc/pgx/v5"
)

// reportSections are the paginated sections of a Report, in display order.
// ID is the section's HTML id, its query parameter prefix (<id>_page,
// <id>_per) and, as <id>-section, the container htmx swaps; Key is its entry
// in the Report template's data. Skipped is only shown with ?show=skipped.
var reportSections = []reportSection{
	{Key: "Changed", ID: "changed", Template: "changedSection"},
	{Key: "New", ID: "new", Template: "eventsSection", Kinds: []string{page.EventNew, page.EventReactivated}}, // pageliterals:ok HTML id
	{Key: "Vanished", ID: "vanished", Template: "eventsSection", Kinds: []string{page.EventVanished}},         // pageliterals:ok HTML id
	{Key: "Retired", ID: "retired", Template: "eventsSection", Kinds: []string{page.EventRetired}},            // pageliterals:ok HTML id
	{Key: "Failures", ID: "failures", Template: "failuresSection"},
	{Key: "Skipped", ID: "skipped", Template: "skippedSection"},
}

type reportSection struct {
	Key, ID, Template string
	Kinds             []string // Page event sections only
}

// sectionView is what a section template renders: one page of its rows.
type sectionView struct {
	ID    string
	Pager pager
	Rows  any
}

// report is one request's view of an ended Check's Report.
type report struct {
	ctx   context.Context
	check dbgen.Check
	proj  dbgen.Project
	url   *url.URL // the Report's URL with the request's query
	diffs *diffIndex
}

func (s *server) newReport(r *http.Request, c dbgen.Check, p dbgen.Project) *report {
	u := *r.URL
	u.Path, u.RawPath, u.Fragment = fmt.Sprintf("/c/%d", c.ID), "", ""
	return &report{ctx: r.Context(), check: c, proj: p, url: &u}
}

// buildReport gathers what the Report template shows for an ended Check.
func (s *server) buildReport(r *http.Request, c dbgen.Check, p dbgen.Project) (map[string]any, error) {
	ctx := r.Context()
	rep := s.newReport(r, c, p)
	data := map[string]any{
		"Project": p, "Check": c,
		"Started": formatTime(c.StartedAt.Time, c.StartedAt.Valid), "Finished": formatTime(c.FinishedAt.Time, c.FinishedAt.Valid),
		"Duration": duration(c.StartedAt, c.FinishedAt),
	}
	var err error
	if data["Dropped"], err = s.q.CountDroppedURLs(ctx, c.ID); err != nil {
		return nil, err
	}
	if c.RobotsFetchedAt.Valid {
		data["RobotsUnreachable"] = check.RobotsUnreachable(int(c.RobotsStatus.Int32), c.RobotsFailureKind.String)
		data["RobotsBody"] = string(c.RobotsBody)
	}
	showSkipped := r.URL.Query().Get("show") == "skipped"
	data["ShowSkipped"] = showSkipped
	for _, sec := range reportSections {
		if sec.ID == "skipped" && !showSkipped {
			continue
		}
		if data[sec.Key], err = s.buildSection(rep, sec); err != nil {
			return nil, err
		}
	}
	idx, err := s.diffIndex(rep)
	if err != nil {
		return nil, err
	}
	data["ChangedCount"], data["UnchangedCount"] = len(idx.changed), idx.unchanged
	return data, nil
}

// reportSectionPage is the htmx fragment of one Report section: only that
// section's queries run. A full load is sent to the Report itself.
func (s *server) reportSectionPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var sec reportSection
	for _, rs := range reportSections {
		if rs.ID == r.PathValue("section") {
			sec = rs
		}
	}
	if sec.ID == "" {
		http.NotFound(w, r)
		return
	}
	c, err := s.q.GetCheck(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	full := fmt.Sprintf("/c/%d?%s#%s", c.ID, r.URL.RawQuery, sec.ID)
	if r.Header.Get("HX-Request") != "true" || check.Active(c.State) {
		sendTo(w, r, full)
		return
	}
	p, err := s.q.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	view, err := s.buildSection(s.newReport(r, c, p), sec)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.fragment(w, r, sec.Template, view)
}

// buildSection loads one page of a Report section: its total first, then
// only the rows on the current page.
func (s *server) buildSection(rep *report, sec reportSection) (sectionView, error) {
	ctx, id := rep.ctx, rep.check.ID
	var (
		total int64
		rows  any
		err   error
	)
	var pg pager
	switch sec.ID {
	case "changed":
		idx, err := s.diffIndex(rep)
		if err != nil {
			return sectionView{}, err
		}
		total = int64(len(idx.changed))
		pg = paginateSection(rep.url, sec.ID, int(total))
		rows, err = s.changedRows(rep, idx.changed[pg.Offset():pg.End(int(total))])
		if err != nil {
			return sectionView{}, err
		}
	case "failures":
		if total, err = s.q.CountFetchFailures(ctx, id); err != nil {
			return sectionView{}, err
		}
		pg = paginateSection(rep.url, sec.ID, int(total))
		rows, err = s.q.ListFetchFailures(ctx, dbgen.ListFetchFailuresParams{CheckID: id, RowLimit: int32(pg.Per), RowOffset: int32(pg.Offset())})
	case "skipped":
		if total, err = s.q.CountSkippedFetches(ctx, id); err != nil {
			return sectionView{}, err
		}
		pg = paginateSection(rep.url, sec.ID, int(total))
		rows, err = s.q.ListSkippedFetches(ctx, dbgen.ListSkippedFetchesParams{CheckID: id, RowLimit: int32(pg.Per), RowOffset: int32(pg.Offset())})
	default:
		if total, err = s.q.CountPageEvents(ctx, dbgen.CountPageEventsParams{CheckID: id, Kinds: sec.Kinds}); err != nil {
			return sectionView{}, err
		}
		pg = paginateSection(rep.url, sec.ID, int(total))
		rows, err = s.q.ListPageEvents(ctx, dbgen.ListPageEventsParams{
			CheckID: id, Kinds: sec.Kinds, RowLimit: int32(pg.Per), RowOffset: int32(pg.Offset()),
		})
	}
	if err != nil {
		return sectionView{}, err
	}
	return sectionView{ID: sec.ID, Pager: pg, Rows: rows}, nil
}

// changedRow is one Page in the Changed Pages section.
type changedRow struct {
	SnapshotID int64
	URL        string
	Tier       string
	Badges     []string
	SEO        []string // "field: before → after"
	Chain      string   // redirect chain, empty when the Page does not redirect
}

// checkChain rebuilds the redirect chain starting at url from one Check's
// Snapshots. Page history shows chains through the same function.
func (s *server) checkChain(ctx context.Context, checkID, projectID int64, host, url string) (redirect.Chain, error) {
	var lookupErr error
	chain := redirect.BuildChain(url, host, func(u string) (redirect.Snap, bool) {
		row, err := s.q.CheckSnapshotRedirect(ctx, dbgen.CheckSnapshotRedirectParams{CheckID: checkID, ProjectID: projectID, Url: u})
		if errors.Is(err, pgx.ErrNoRows) {
			return redirect.Snap{}, false
		}
		if err != nil {
			lookupErr = err
			return redirect.Snap{}, false
		}
		return redirect.Snap{Status: int(row.Status), Target: row.RedirectTarget}, true
	})
	return chain, lookupErr
}

// diffIndex is a Check's Diffs classified for its Report: the changed ones,
// most significant first, and how many were unchanged. Pages that are now
// ignored or excluded are left out of both.
type diffIndex struct {
	changed   []int64 // snapshot ids
	unchanged int
}

// diffIndex classifies the whole Check once per request from a light query,
// so counts and ranking never depend on the page shown.
func (s *server) diffIndex(rep *report) (*diffIndex, error) {
	if rep.diffs != nil {
		return rep.diffs, nil
	}
	rows, err := s.q.ListCheckDiffStates(rep.ctx, rep.check.ID)
	if err != nil {
		return nil, err
	}
	idx := &diffIndex{}
	for _, d := range rows {
		if d.State == page.StateIgnored || page.Excluded(rep.proj.ExcludePatterns, d.Origin, d.Url) {
			continue
		}
		if d.Tier == string(diff.TierEmpty) {
			idx.unchanged++
			continue
		}
		idx.changed = append(idx.changed, d.SnapshotID)
	}
	rep.diffs = idx
	return idx, nil
}

// changedRows loads and formats the given changed Diffs, in ranked order.
func (s *server) changedRows(rep *report, ids []int64) ([]changedRow, error) {
	rows, err := s.q.ListCheckDiffRows(rep.ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]changedRow, 0, len(rows))
	for _, d := range rows {
		row, err := s.changedRowOf(rep.ctx, rep.check.ID, rep.proj, d)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *server) changedRowOf(ctx context.Context, checkID int64, p dbgen.Project, d dbgen.ListCheckDiffRowsRow) (changedRow, error) {
	row := changedRow{SnapshotID: d.SnapshotID, URL: d.Url, Tier: d.Tier}
	for _, b := range []struct {
		on   bool
		name string
	}{{d.StatusChanged, "status"}, {d.SeoChanged, "SEO"}, {d.HeadersChanged, "headers"}, {d.BodyChanged, "body"}} {
		if b.on {
			row.Badges = append(row.Badges, b.name)
		}
	}
	var changes map[string]diff.SEOChange
	if err := json.Unmarshal(d.SeoChanges, &changes); err != nil {
		return row, err
	}
	for _, k := range seo.Keys {
		if c, ok := changes[k]; ok {
			row.SEO = append(row.SEO, fmt.Sprintf("%s: %s → %s", k, seoValues(c.Before), seoValues(c.After)))
		}
	}
	if d.RedirectTarget != "" {
		chain, err := s.checkChain(ctx, checkID, p.ID, p.Domain, d.Url)
		if err != nil {
			return row, err
		}
		row.Chain = chain.Label()
	}
	return row, nil
}

func seoValues(vs []string) string {
	if len(vs) == 0 {
		return "(none)"
	}
	return strings.Join(vs, " | ")
}
