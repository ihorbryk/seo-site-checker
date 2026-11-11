package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/urlnorm"
	"github.com/jackc/pgx/v5"
)

// poolFilters are the Page pool tab's filters, in display order; "" is all.
var poolFilters = []struct{ Key, Label string }{
	{"", "All"}, {page.StateActive, "Active"}, {"orphaned", "Orphaned"},
	{page.StateRetired, "Retired"}, {page.StateIgnored, "Ignored"}, {"excluded", "Excluded"},
}

// pagePoolState is what poolState needs to classify a Page.
type pagePoolState struct {
	State, Origin, URL string
	OrphanStreak       int32
	ExcludePatterns    []string
}

// poolState classifies a Page for display: the filter key it belongs to
// (orphaned Pages are also active; excluded ones are not) and its label.
// Exclusion is computed on render, never stored; the seed Page is never excluded.
func poolState(pg pagePoolState) (key, label string) {
	switch {
	case pg.State == page.StateActive && page.Excluded(pg.ExcludePatterns, pg.Origin, pg.URL):
		return "excluded", "matches exclude pattern"
	case pg.State == page.StateActive && pg.OrphanStreak > 0:
		return "orphaned", fmt.Sprintf("orphaned %d/%d", pg.OrphanStreak, page.RetireAfter)
	default:
		return pg.State, pg.State
	}
}

type poolRow struct {
	ID            int64
	URL, State    string
	LastStatus    int32
	LastChangedID int64
	LastChanged   string
	Snapshots     int64
	FirstCheck    int64
}

func (s *server) projectPages(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	s.renderPool(w, r, p, nil)
}

// renderPool writes the Page pool tab; flash, if not nil, is shown above the
// Add Page form.
func (s *server) renderPool(w http.ResponseWriter, r *http.Request, p dbgen.Project, flash map[string]any) {
	// The whole pool is classified in Go (exclusion is never stored) for the
	// filter counts and the filtered total; only the Pages on the current page
	// load the costly columns.
	states, err := s.q.ListPagePoolStates(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	filter := r.URL.Query().Get("state")
	known := false
	for _, f := range poolFilters {
		known = known || f.Key == filter
	}
	if !known {
		filter = ""
	}
	counts := map[string]int{}
	labels := map[int64]string{}
	var ids []int64
	for _, pg := range states {
		key, label := poolState(pagePoolState{State: pg.State, Origin: pg.Origin, URL: pg.Url, OrphanStreak: pg.OrphanStreak, ExcludePatterns: p.ExcludePatterns})
		counts[""]++
		counts[key]++
		if key == "orphaned" {
			counts[page.StateActive]++
		}
		if filter != "" && key != filter && !(filter == page.StateActive && key == "orphaned") {
			continue
		}
		ids = append(ids, pg.ID)
		labels[pg.ID] = label
	}
	pager := paginate(r.URL, len(ids))
	pages, err := s.q.ListPagePoolRows(r.Context(), ids[pager.Offset():pager.End(len(ids))])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows := make([]poolRow, 0, len(pages))
	for _, pg := range pages {
		row := poolRow{
			ID: pg.ID, URL: pg.Url, State: labels[pg.ID], LastStatus: pg.LastStatus,
			Snapshots: pg.Snapshots, FirstCheck: pg.FirstCheckID, LastChangedID: pg.LastChangedSnapshotID,
		}
		if pg.LastChangedSnapshotID != 0 {
			row.LastChanged = formatTime(pg.LastChangedAt, true)
		}
		rows = append(rows, row)
	}
	type filterView struct {
		Label, Href string
		Count       int
		Current     bool
	}
	var filters []filterView
	for _, f := range poolFilters {
		filters = append(filters, filterView{f.Label, pager.With("state", f.Key), counts[f.Key], f.Key == filter})
	}
	data := map[string]any{"Project": p, "Tab": "pages", "Pages": rows, "Filters": filters, "Flash": flash, "Pager": pager}
	s.render(w, r, "pages", p.Domain, projectCrumbs(p, crumb{Label: "Page pool"}), data)
}

// addPage adds a Page by hand: any URL on the Project's exact domain.
func (s *server) addPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.q.GetProject(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	reject := func(msg string) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.fragment(w, r, "flash", map[string]any{"Err": true, "Message": msg})
	}
	raw := strings.TrimSpace(r.PostFormValue("url"))
	if u, err := url.Parse(raw); err != nil || !u.IsAbs() || u.Host == "" {
		reject(fmt.Sprintf("%q is not a full URL on %s", raw, p.Domain))
		return
	}
	norm, err := urlnorm.Normalize(raw)
	if err != nil {
		reject(fmt.Sprintf("%q is not a valid http(s) URL", raw))
		return
	}
	if urlnorm.Host(norm) != p.Domain {
		reject(fmt.Sprintf("%s is not on %s (exactly this host; subdomains are separate Projects)", norm, p.Domain))
		return
	}
	if v := urlnorm.Violation(norm); v != "" {
		reject(fmt.Sprintf("%s is refused (%s)", norm, strings.ReplaceAll(v, "_", " ")))
		return
	}
	pg, err := s.q.AddManualPage(r.Context(), dbgen.AddManualPageParams{ProjectID: p.ID, Url: norm})
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		reject(fmt.Sprintf("%s is ignored; restore it from its history page instead", norm))
		return
	default:
		s.fail(w, r, err)
		return
	}
	sendTo(w, r, fmt.Sprintf("/p/%d/pages", pg.ProjectID))
}

func (s *server) ignorePage(w http.ResponseWriter, r *http.Request) {
	s.setPageState(w, r, true)
}

func (s *server) restorePage(w http.ResponseWriter, r *http.Request) {
	s.setPageState(w, r, false)
}

func (s *server) setPageState(w http.ResponseWriter, r *http.Request, ignore bool) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	pg, err := s.q.GetPage(r.Context(), id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	if ignore && pg.Origin == page.OriginSeed {
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.fragment(w, r, "flash", map[string]any{"Err": true, "Message": "The seed Page cannot be ignored."})
		return
	}
	if ignore {
		_, err = s.q.IgnorePage(r.Context(), id)
	} else {
		_, err = s.q.RestorePage(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sendTo(w, r, fmt.Sprintf("/pg/%d", id))
}

type historyRow struct {
	CheckID    int64
	Started    string
	Result     string
	Tier       string
	Badges     []string
	Note       string // "empty", "first Snapshot" or ""
	SnapshotID int64
	DiffLink   bool
	Events     []string
	Chain      string // redirect chain, empty when the Snapshot is not a redirect
}

func (s *server) pageHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	pg, err := s.q.GetPage(ctx, id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	total, err := s.q.CountPageHistory(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pager := paginate(r.URL, int(total))
	hist, err := s.q.ListPageHistory(ctx, dbgen.ListPageHistoryParams{
		PageID: id, RowLimit: int32(pager.Per), RowOffset: int32(pager.Offset()),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows := make([]historyRow, 0, len(hist))
	for _, h := range hist {
		row := historyRow{
			CheckID: h.CheckID, Started: formatTime(h.CheckStartedAt.Time, h.CheckStartedAt.Valid),
			Events: h.Events, SnapshotID: h.SnapshotID.Int64,
		}
		switch h.Outcome.String {
		case "snapshot":
			row.Result = fmt.Sprintf("%d", h.Status.Int32)
			if h.DurationMs.Valid {
				row.Result += fmt.Sprintf(" · %d ms", h.DurationMs.Int32)
			}
			if h.BodySize.Valid {
				row.Result += fmt.Sprintf(" · %d bytes", h.BodySize.Int32)
			}
			if h.Tier.Valid {
				row.Tier, row.DiffLink = h.Tier.String, true
				if h.Tier.String == "empty" {
					row.Note = "empty"
				}
				for _, b := range []struct {
					on   bool
					name string
				}{{h.StatusChanged.Bool, "status"}, {h.SeoChanged.Bool, "SEO"}, {h.HeadersChanged.Bool, "headers"}, {h.BodyChanged.Bool, "body"}} {
					if b.on {
						row.Badges = append(row.Badges, b.name)
					}
				}
			} else {
				row.Note = "first Snapshot"
			}
			if h.Status.Int32 >= 300 && h.Status.Int32 < 400 {
				chain, err := s.checkChain(ctx, h.CheckID, pg.ProjectID, pg.Domain, pg.Url)
				if err != nil {
					s.fail(w, r, err)
					return
				}
				row.Chain = chain.Label()
			}
		case "failure":
			row.Result = "failed: " + h.FailureKind.String
			if h.Message.Valid && h.Message.String != "" {
				row.Result += " (" + h.Message.String + ")"
			}
		default:
			row.Result = "skipped: " + strings.ReplaceAll(h.SkipReason.String, "_", " ")
		}
		rows = append(rows, row)
	}
	_, label := poolState(pagePoolState{State: pg.State, Origin: pg.Origin, URL: pg.Url, OrphanStreak: pg.OrphanStreak, ExcludePatterns: pg.ExcludePatterns})
	first := ""
	if pg.FirstCheckID != 0 {
		first = fmt.Sprintf("%d", pg.FirstCheckID)
	}
	crumbs := projectCrumbs(dbgen.Project{ID: pg.ProjectID, Domain: pg.Domain},
		crumb{Label: "Page pool", Href: fmt.Sprintf("/p/%d/pages", pg.ProjectID)}, crumb{Label: pg.Url})
	s.render(w, r, "pageHistory", "Page history", crumbs, map[string]any{
		"Page": pg, "State": label, "FirstCheck": first, "History": rows, "Pager": pager,
		"CanIgnore":  pg.Origin != page.OriginSeed && pg.State != page.StateIgnored,
		"CanRestore": pg.State == page.StateIgnored,
	})
}
