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
}

func (s *server) newReport(r *http.Request, c dbgen.Check, p dbgen.Project) *report {
	u := *r.URL
	u.Path, u.RawPath, u.Fragment = checkPath(c.ProjectID, c.Number), "", ""
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
	// The counts were stored when the Check ended (ADR 0008).
	data["ChangedCount"] = int(c.CountDiffCritical) + int(c.CountDiffSeo) + int(c.CountDiffHeaders) + int(c.CountDiffBody)
	data["UnchangedCount"] = int(c.CountDiffEmpty)
	return data, nil
}

// reportSectionPage is the htmx fragment of one Report section: only that
// section's queries run. A full load is sent to the Report itself.
func (s *server) reportSectionPage(w http.ResponseWriter, r *http.Request) {
	c, ok := s.pathCheck(w, r)
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
	full := fmt.Sprintf("%s?%s#%s", checkPath(c.ProjectID, c.Number), r.URL.RawQuery, sec.ID)
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

// denseFactor decides how a section is read. A section of n rows next to a
// pool of Fetches is dense when n*denseFactor >= pool: it is read by walking
// the Project's Pages in URL order and probing each for a row of the Check,
// which takes about pool/n probes per row. A sparser section is read from an
// index of its own rows and sorted, which takes at most pool/denseFactor rows.
// Either way the first page of a 2M-Page Check costs about 10,000 index
// probes or less (ADR 0010).
const denseFactor = 100

// dense reports whether a section of n rows is read by walking the Pages. The
// pool is the Check's own Fetches, stored when it ended.
func (rep *report) dense(n int64) bool {
	c := rep.check
	pool := int64(c.CountSnapshots) + int64(c.CountFailures) + int64(c.CountSkipped)
	return n*denseFactor >= pool
}

// buildSection loads one page of a Report section: its total from the counts
// stored on the Check (ADR 0008), then only the rows on the current page.
func (s *server) buildSection(rep *report, sec reportSection) (sectionView, error) {
	c := rep.check
	var (
		total int64
		rows  any
		err   error
	)
	var pg pager
	page := func() (limit, offset int32) {
		pg = paginateSection(rep.url, sec.ID, int(total))
		return int32(pg.Per), int32(pg.Offset())
	}
	switch sec.ID {
	case "changed":
		tiers := rep.changedTiers()
		for _, t := range tiers {
			total += t.count
		}
		pg = paginateSection(rep.url, sec.ID, int(total))
		rows, err = s.changedRows(rep, tiers, int64(pg.Offset()), int64(pg.End(int(total))))
	case "failures":
		total = int64(c.CountFailures)
		limit, offset := page()
		rows, err = s.failureRows(rep, limit, offset)
	case "skipped":
		total = int64(c.CountSkipped)
		limit, offset := page()
		rows, err = s.skippedRows(rep, limit, offset)
	default:
		total = map[string]int64{"new": int64(c.CountNew), "vanished": int64(c.CountVanished), "retired": int64(c.CountRetired)}[sec.ID]
		limit, offset := page()
		rows, err = s.eventRows(rep, sec, total, limit, offset)
	}
	if err != nil {
		return sectionView{}, err
	}
	return sectionView{ID: sec.ID, Pager: pg, Rows: rows}, nil
}

func (s *server) failureRows(rep *report, limit, offset int32) ([]dbgen.ListFetchFailuresDenseRow, error) {
	c := rep.check
	if rep.dense(int64(c.CountFailures)) {
		return s.q.ListFetchFailuresDense(rep.ctx, dbgen.ListFetchFailuresDenseParams{CheckID: c.ID, ProjectID: c.ProjectID, RowLimit: limit, RowOffset: offset})
	}
	rows, err := s.q.ListFetchFailuresSparse(rep.ctx, dbgen.ListFetchFailuresSparseParams{CheckID: c.ID, RowLimit: limit, RowOffset: offset})
	out := make([]dbgen.ListFetchFailuresDenseRow, len(rows))
	for i, r := range rows {
		out[i] = dbgen.ListFetchFailuresDenseRow(r)
	}
	return out, err
}

func (s *server) skippedRows(rep *report, limit, offset int32) ([]dbgen.ListSkippedFetchesDenseRow, error) {
	c := rep.check
	if rep.dense(int64(c.CountSkipped)) {
		return s.q.ListSkippedFetchesDense(rep.ctx, dbgen.ListSkippedFetchesDenseParams{CheckID: c.ID, ProjectID: c.ProjectID, RowLimit: limit, RowOffset: offset})
	}
	rows, err := s.q.ListSkippedFetchesSparse(rep.ctx, dbgen.ListSkippedFetchesSparseParams{CheckID: c.ID, RowLimit: limit, RowOffset: offset})
	out := make([]dbgen.ListSkippedFetchesDenseRow, len(rows))
	for i, r := range rows {
		out[i] = dbgen.ListSkippedFetchesDenseRow(r)
	}
	return out, err
}

func (s *server) eventRows(rep *report, sec reportSection, total int64, limit, offset int32) ([]dbgen.ListPageEventsDenseRow, error) {
	c := rep.check
	if rep.dense(total) {
		return s.q.ListPageEventsDense(rep.ctx, dbgen.ListPageEventsDenseParams{CheckID: c.ID, ProjectID: c.ProjectID, Kinds: sec.Kinds, RowLimit: limit, RowOffset: offset})
	}
	rows, err := s.q.ListPageEventsSparse(rep.ctx, dbgen.ListPageEventsSparseParams{CheckID: c.ID, Kinds: sec.Kinds, RowLimit: limit, RowOffset: offset})
	out := make([]dbgen.ListPageEventsDenseRow, len(rows))
	for i, r := range rows {
		out[i] = dbgen.ListPageEventsDenseRow(r)
	}
	return out, err
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

// changedTier is one tier of the Changed section and how many Diffs the Check
// has in it.
type changedTier struct {
	name  string
	count int64
}

// changedTiers are the tiers of the Changed section, most significant first,
// with the counts stored when the Check ended. Every Diff of the Check is
// counted, whatever its Page's state is now: a Report is fixed when its Check
// ends. This departs from story 78 of #19 (leave ignored and excluded Pages
// out of the Report) for Pages ignored or excluded after the Check; see ADR 0008.
func (rep *report) changedTiers() []changedTier {
	c := rep.check
	return []changedTier{
		{string(diff.TierCritical), int64(c.CountDiffCritical)},
		{string(diff.TierSEO), int64(c.CountDiffSeo)},
		{string(diff.TierHeaders), int64(c.CountDiffHeaders)},
		{string(diff.TierBody), int64(c.CountDiffBody)},
	}
}

// changedRows loads and formats the Changed rows from..to (positions in the
// section, which runs through the tiers in order). Each tier the page touches
// is read at its own offset, so a page can span two tiers.
func (s *server) changedRows(rep *report, tiers []changedTier, from, to int64) ([]changedRow, error) {
	var ids []int64
	var start int64
	for _, t := range tiers {
		lo, hi := max(from, start), min(to, start+t.count)
		if lo < hi {
			tierIDs, err := s.changedIDs(rep, t, lo-start, hi-lo)
			if err != nil {
				return nil, err
			}
			ids = append(ids, tierIDs...)
		}
		start += t.count
	}
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

// changedIDs is the Snapshot ids of one page of a tier, in URL order.
func (s *server) changedIDs(rep *report, t changedTier, offset, limit int64) ([]int64, error) {
	c := rep.check
	if rep.dense(t.count) {
		return s.q.ListChangedSnapshotIDsDense(rep.ctx, dbgen.ListChangedSnapshotIDsDenseParams{
			CheckID: c.ID, ProjectID: c.ProjectID, Tier: t.name, RowLimit: int32(limit), RowOffset: int32(offset)})
	}
	return s.q.ListChangedSnapshotIDsSparse(rep.ctx, dbgen.ListChangedSnapshotIDsSparseParams{
		CheckID: c.ID, Tier: t.name, RowLimit: int32(limit), RowOffset: int32(offset)})
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
