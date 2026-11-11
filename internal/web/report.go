package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/redirect"
	"github.com/ihorbryk/seo-site-checker/internal/seo"
	"github.com/jackc/pgx/v5"
)

// reportSections are the Page event sections of a Report, in display order
// after Changed Pages (a `new` section also lists reactivated Pages).
var reportSections = []struct {
	Key, ID string
	Kinds   []string
}{
	{"New", "new", []string{page.EventNew, page.EventReactivated}},
	{"Vanished", "vanished", []string{page.EventVanished}},
	{"Retired", "retired", []string{page.EventRetired}},
}

// eventSection is one Page event section of the Report.
type eventSection struct {
	ID   string
	Rows []dbgen.ListPageEventsRow
}

// buildReport gathers what the Report template shows for an ended Check.
func (s *server) buildReport(r *http.Request, c dbgen.Check, p dbgen.Project) (map[string]any, error) {
	ctx := r.Context()
	data := map[string]any{
		"Project": p, "Check": c,
		"Started": formatTime(c.StartedAt.Time, c.StartedAt.Valid), "Finished": formatTime(c.FinishedAt.Time, c.FinishedAt.Valid),
		"Duration": duration(c.StartedAt, c.FinishedAt),
	}
	failures, err := s.q.ListFetchFailures(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	data["Failures"] = failures
	if data["Dropped"], err = s.q.CountDroppedURLs(ctx, c.ID); err != nil {
		return nil, err
	}
	if c.RobotsFetchedAt.Valid {
		data["RobotsUnreachable"] = check.RobotsUnreachable(int(c.RobotsStatus.Int32), c.RobotsFailureKind.String)
		data["RobotsBody"] = string(c.RobotsBody)
	}
	if r.URL.Query().Get("show") == "skipped" {
		skipped, err := s.q.ListSkippedFetches(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		data["ShowSkipped"] = true
		data["Skipped"] = skipped
	}
	for _, sec := range reportSections {
		rows, err := s.q.ListPageEvents(ctx, dbgen.ListPageEventsParams{CheckID: c.ID, Kinds: sec.Kinds})
		if err != nil {
			return nil, err
		}
		data[sec.Key] = eventSection{ID: sec.ID, Rows: rows}
	}
	changed, unchanged, err := s.changedPages(r, c, p)
	if err != nil {
		return nil, err
	}
	data["Changed"] = changed
	data["ChangedCount"], data["UnchangedCount"] = len(changed), unchanged
	return data, nil
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

// changedPages returns the Check's changed Pages, most significant first, and
// how many Pages were unchanged. Pages that are now ignored or excluded are
// left out of both.
func (s *server) changedPages(r *http.Request, c dbgen.Check, p dbgen.Project) (changed []changedRow, unchanged int, err error) {
	rows, err := s.q.ListCheckDiffs(r.Context(), c.ID)
	if err != nil {
		return nil, 0, err
	}
	for _, d := range rows {
		if d.State == page.StateIgnored || page.Excluded(p.ExcludePatterns, d.Origin, d.Url) {
			continue
		}
		if d.Tier == string(diff.TierEmpty) {
			unchanged++
			continue
		}
		row, err := s.changedRowOf(r.Context(), c.ID, p, d)
		if err != nil {
			return nil, 0, err
		}
		changed = append(changed, row)
	}
	return changed, unchanged, nil
}

func (s *server) changedRowOf(ctx context.Context, checkID int64, p dbgen.Project, d dbgen.ListCheckDiffsRow) (changedRow, error) {
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
