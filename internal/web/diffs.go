package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/ihorbryk/seo-site-checker/internal/seo"
	"github.com/jackc/pgx/v5"
)

type diffSEORow struct {
	Name          string
	Before, After string
}

type diffHeaderRow struct {
	Kind          string
	Name          string
	Before, After []string
}

// diffSnapshot is a Snapshot loaded for a Diff.
type diffSnapshot struct {
	view    dbgen.GetSnapshotViewRow
	headers [][2]string
	fields  seo.Fields
}

func (s *server) loadDiffSnapshot(r *http.Request, id int64) (diffSnapshot, error) {
	v, err := s.q.GetSnapshotView(r.Context(), id)
	if err != nil {
		return diffSnapshot{}, err
	}
	d := diffSnapshot{view: v}
	if err := json.Unmarshal(v.Headers, &d.headers); err != nil {
		return d, err
	}
	err = json.Unmarshal(v.SeoFields, &d.fields)
	return d, err
}

func (d diffSnapshot) side(s *server, r *http.Request) diff.Side {
	return diff.Side{
		Headers: d.headers, SEO: d.fields, BodySHA256: d.view.BodySha256,
		LoadBody: func() ([]byte, error) {
			return s.q.GetBodyContent(r.Context(), dbgen.GetBodyContentParams{ProjectID: d.view.ProjectID, Sha256: d.view.BodySha256})
		},
	}
}

// diffPage is the full Diff of a Snapshot against its Page's previous
// Snapshot, computed on view from the two stored Snapshots and the Check's
// frozen Noise rules, so it agrees with the stored summary. {id} is the later
// Snapshot's id. With the htmx header and a diff= mode it returns only the
// body section, which is what the split/unified toggle swaps in.
func (s *server) diffPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	after, err := s.loadDiffSnapshot(r, id)
	if err != nil {
		s.notFoundOrFail(w, r, err)
		return
	}
	snap := after.view
	check, err := s.q.GetCheck(ctx, snap.CheckID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path := snap.Url
	if u, err := url.Parse(snap.Url); err == nil && u.RequestURI() != "" {
		path = u.RequestURI()
	}
	crumbs := checkCrumbs(dbgen.Project{ID: snap.ProjectID, Domain: snap.Domain}, snap.CheckID, crumb{Label: "Diff " + path})
	data := map[string]any{
		"After": snap, "Path": path, "AfterFetched": formatTime(snap.FetchedAt, true),
		// The Check whose frozen Noise rules this Diff used (shown by #31).
		"NoiseCheckID": check.ID,
	}
	title := "Diff " + path

	baselineID, err := s.q.GetDiffBaseline(ctx, id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.fail(w, r, err)
			return
		}
		data["First"] = true
		s.render(w, r, "diff", title, crumbs, data)
		return
	}
	before, err := s.loadDiffSnapshot(r, baselineID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rules, err := diff.ParseRules(check.NoiseRules)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data["Before"] = before.view
	data["BeforeFetched"] = formatTime(before.view.FetchedAt, true)

	var headerRows []diffHeaderRow
	for _, h := range diff.CompareHeaders(before.headers, after.headers, rules.IgnoredHeaders()) {
		headerRows = append(headerRows, diffHeaderRow{Kind: string(h.Kind), Name: h.Name, Before: h.Before, After: h.After})
	}
	data["Headers"] = headerRows
	// Ignored: header names that differ but are masked by Noise rules.
	var ignored []string
	kept := map[string]bool{}
	for _, h := range diff.CompareHeaders(before.headers, after.headers, rules.IgnoredHeaders()) {
		kept[h.Name] = true
	}
	for _, h := range diff.CompareHeaders(before.headers, after.headers, nil) {
		if !kept[h.Name] {
			ignored = append(ignored, h.Name)
		}
	}
	data["Ignored"] = strings.Join(ignored, ", ")

	changes := diff.CompareSEO(before.fields, after.fields)
	var seoRows []diffSEORow
	for _, k := range seo.Keys {
		if c, ok := changes[k]; ok {
			seoRows = append(seoRows, diffSEORow{Name: k, Before: seoValues(c.Before), After: seoValues(c.After)})
		}
	}
	data["SEO"] = seoRows

	bodyChanged := false
	switch {
	case before.view.BodySha256 == nil && after.view.BodySha256 == nil:
	case before.view.BodySha256 == nil:
		data["BodyNote"], bodyChanged = "body added", true
	case after.view.BodySha256 == nil:
		data["BodyNote"], bodyChanged = "body removed", true
	default:
		b, err := diff.MaskedBody(before.headers, before.side(s, r).LoadBody, rules)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		a, err := diff.MaskedBody(after.headers, after.side(s, r).LoadBody, rules)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		rows := diff.LineDiff(b, a, diff.ContextLines)
		bodyChanged = len(rows) > 0
		mode := r.URL.Query().Get("diff")
		if mode != "unified" {
			mode = "split"
		}
		data["Mode"] = mode
		data["Unified"], data["Split"] = rows, diff.SplitRows(rows)
	}
	data["BodyChanged"] = bodyChanged
	data["Empty"] = len(seoRows) == 0 && len(headerRows) == 0 && !bodyChanged
	s.render(w, r, "diff", title, crumbs, data)
}
