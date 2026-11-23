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
	{Key: "New", ID: "new", Template: "eventsSection", Kinds: []string{page.EventNew, page.EventReactivated}, Count: func(c dbgen.Check) int32 { return c.CountNew }}, // pageliterals:ok HTML id
	{Key: "Vanished", ID: "vanished", Template: "eventsSection", Kinds: []string{page.EventVanished}, Count: func(c dbgen.Check) int32 { return c.CountVanished }},    // pageliterals:ok HTML id
	{Key: "Retired", ID: "retired", Template: "eventsSection", Kinds: []string{page.EventRetired}, Count: func(c dbgen.Check) int32 { return c.CountRetired }},        // pageliterals:ok HTML id
	{Key: "Failures", ID: "failures", Template: "failuresSection"},
	{Key: "Skipped", ID: "skipped", Template: "skippedSection"},
}

type reportSection struct {
	Key, ID, Template string
	Kinds             []string                // Page event sections only
	Count             func(dbgen.Check) int32 // Page event sections only: the total stored on the Check
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
	counts := rep.counts()
	data["ChangedCount"] = int(counts.changed())
	data["UnchangedCount"] = int(counts.Unchanged)
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

// reportCounts is what a Report reads from the counts stored on its Check
// when it ended (ADR 0008, 0010). Nothing else in the web app derives a Report
// number from the Check's columns.
type reportCounts struct {
	Critical, SEO, Headers, Body int64 // Diffs by tier
	Unchanged                    int64
	pool                         int64 // the Pages the dense walk steps over
}

func (rep *report) counts() reportCounts {
	c := rep.check
	pool := int64(c.CountPages)
	if pool == 0 {
		// A Check that ended before count_pages was stored: its own Fetches.
		pool = int64(c.CountSnapshots) + int64(c.CountFailures) + int64(c.CountSkipped)
	}
	return reportCounts{
		Critical: int64(c.CountDiffCritical), SEO: int64(c.CountDiffSeo),
		Headers: int64(c.CountDiffHeaders), Body: int64(c.CountDiffBody),
		Unchanged: int64(c.CountDiffEmpty), pool: pool,
	}
}

// changed is the Changed Pages total: every tier but empty.
func (rc reportCounts) changed() int64 { return rc.Critical + rc.SEO + rc.Headers + rc.Body }

// dense reports whether a section of n rows is read by walking the Pages. The
// pool is the number of Pages the walk steps over, stored when the Check
// ended: retired, ignored and excluded Pages get no Fetch but are walked too.
func (rc reportCounts) dense(n int64) bool { return n*denseFactor >= rc.pool }

func (rep *report) dense(n int64) bool { return rep.counts().dense(n) }

// buildSection loads one page of a Report section: its total from the counts
// stored on the Check (ADR 0008), then only the rows on the current page.
func (s *server) buildSection(rep *report, sec reportSection) (sectionView, error) {
	c := rep.check
	var (
		pg   pager
		rows any
		err  error
	)
	// paged sets the section's pager for its total and returns the window of
	// rows on the current page.
	paged := func(total int64) window {
		pg = paginateSection(rep.url, sec.ID, int(total))
		return pg.Window()
	}
	switch sec.ID {
	case "changed":
		tiers := rep.changedTiers()
		rows, err = s.changedRows(rep, tiers, paged(rep.counts().changed()))
	case "failures":
		w := paged(int64(c.CountFailures))
		rows, err = sectionRows(rep.dense(int64(c.CountFailures)),
			func() ([]dbgen.ListFetchFailuresDenseRow, error) {
				return s.q.ListFetchFailuresDense(rep.ctx, dbgen.ListFetchFailuresDenseParams{CheckID: c.ID, ProjectID: c.ProjectID, RowLimit: w.Limit, RowOffset: w.Offset})
			},
			func() ([]dbgen.ListFetchFailuresSparseRow, error) {
				return s.q.ListFetchFailuresSparse(rep.ctx, dbgen.ListFetchFailuresSparseParams{CheckID: c.ID, RowLimit: w.Limit, RowOffset: w.Offset})
			},
			func(r dbgen.ListFetchFailuresSparseRow) dbgen.ListFetchFailuresDenseRow {
				return dbgen.ListFetchFailuresDenseRow(r)
			})
	case "skipped":
		w := paged(int64(c.CountSkipped))
		rows, err = sectionRows(rep.dense(int64(c.CountSkipped)),
			func() ([]dbgen.ListSkippedFetchesDenseRow, error) {
				return s.q.ListSkippedFetchesDense(rep.ctx, dbgen.ListSkippedFetchesDenseParams{CheckID: c.ID, ProjectID: c.ProjectID, RowLimit: w.Limit, RowOffset: w.Offset})
			},
			func() ([]dbgen.ListSkippedFetchesSparseRow, error) {
				return s.q.ListSkippedFetchesSparse(rep.ctx, dbgen.ListSkippedFetchesSparseParams{CheckID: c.ID, RowLimit: w.Limit, RowOffset: w.Offset})
			},
			func(r dbgen.ListSkippedFetchesSparseRow) dbgen.ListSkippedFetchesDenseRow {
				return dbgen.ListSkippedFetchesDenseRow(r)
			})
	default:
		total := int64(sec.Count(c))
		w := paged(total)
		rows, err = sectionRows(rep.dense(total),
			func() ([]dbgen.ListPageEventsDenseRow, error) {
				return s.q.ListPageEventsDense(rep.ctx, dbgen.ListPageEventsDenseParams{CheckID: c.ID, ProjectID: c.ProjectID, Kinds: sec.Kinds, RowLimit: w.Limit, RowOffset: w.Offset})
			},
			func() ([]dbgen.ListPageEventsSparseRow, error) {
				return s.q.ListPageEventsSparse(rep.ctx, dbgen.ListPageEventsSparseParams{CheckID: c.ID, Kinds: sec.Kinds, RowLimit: w.Limit, RowOffset: w.Offset})
			},
			func(r dbgen.ListPageEventsSparseRow) dbgen.ListPageEventsDenseRow {
				return dbgen.ListPageEventsDenseRow(r)
			})
	}
	if err != nil {
		return sectionView{}, err
	}
	return sectionView{ID: sec.ID, Pager: pg, Rows: rows}, nil
}

// sectionRows reads a page of a section the dense way or the sparse way (ADR
// 0010). The two queries select the same columns, so conv turns a sparse row
// into the dense row type the templates read. On error it returns no rows.
func sectionRows[D, S any](dense bool, listDense func() ([]D, error), listSparse func() ([]S, error), conv func(S) D) ([]D, error) {
	if dense {
		return listDense()
	}
	sparse, err := listSparse()
	if err != nil {
		return nil, err
	}
	out := make([]D, len(sparse))
	for i, r := range sparse {
		out[i] = conv(r)
	}
	return out, nil
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
	rc := rep.counts()
	return []changedTier{
		{string(diff.TierCritical), rc.Critical},
		{string(diff.TierSEO), rc.SEO},
		{string(diff.TierHeaders), rc.Headers},
		{string(diff.TierBody), rc.Body},
	}
}

// changedRows loads and formats the Changed rows in w (positions in the
// section, which runs through the tiers in order). Each tier the page touches
// is read at its own offset, so a page can span two tiers.
func (s *server) changedRows(rep *report, tiers []changedTier, w window) ([]changedRow, error) {
	var ids []int64
	var start int64
	from, to := int64(w.Offset), int64(w.Offset)+int64(w.Limit)
	for _, t := range tiers {
		lo, hi := max(from, start), min(to, start+t.count)
		if lo < hi {
			tierIDs, err := s.changedIDs(rep, t, window{Offset: int32(lo - start), Limit: int32(hi - lo)})
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
func (s *server) changedIDs(rep *report, t changedTier, w window) ([]int64, error) {
	c := rep.check
	return sectionRows(rep.dense(t.count),
		func() ([]int64, error) {
			return s.q.ListChangedSnapshotIDsDense(rep.ctx, dbgen.ListChangedSnapshotIDsDenseParams{
				CheckID: c.ID, ProjectID: c.ProjectID, Tier: t.name, RowLimit: w.Limit, RowOffset: w.Offset})
		},
		func() ([]int64, error) {
			return s.q.ListChangedSnapshotIDsSparse(rep.ctx, dbgen.ListChangedSnapshotIDsSparseParams{
				CheckID: c.ID, Tier: t.name, RowLimit: w.Limit, RowOffset: w.Offset})
		},
		func(id int64) int64 { return id })
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
