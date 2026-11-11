package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/seo"
	"github.com/jackc/pgx/v5"
)

type seoRow struct {
	Name   string
	Values []string
}

// snapshotPage is the raw Snapshot: what the server sent, as stored.
func (s *server) snapshotPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	snap, err := s.q.GetSnapshotView(ctx, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}

	var headers [][2]string
	var fields seo.Fields
	if err := json.Unmarshal(snap.Headers, &headers); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := json.Unmarshal(snap.SeoFields, &fields); err != nil {
		s.fail(w, r, err)
		return
	}
	var rows []seoRow
	for _, k := range seo.Keys {
		rows = append(rows, seoRow{Name: k, Values: fields[k]})
	}

	tab := r.URL.Query().Get("tab")
	if tab != "headers" && tab != "html" {
		tab = "seo"
	}
	data := map[string]any{
		"Snap": snap, "Tab": tab, "SEO": rows, "Headers": headers,
		"Fetched": formatTime(snap.FetchedAt, true),
		"Timing":  "—", "Size": "—", "HasBody": snap.BodySha256 != nil,
	}
	if snap.DurationMs.Valid {
		data["Timing"] = fmt.Sprintf("%d ms", snap.DurationMs.Int32)
	}
	if snap.BodySize.Valid {
		data["Size"] = fmt.Sprintf("%d bytes", snap.BodySize.Int32)
	}
	if tab == "html" && snap.BodySha256 != nil {
		content, err := s.q.GetBodyContent(ctx, dbgen.GetBodyContentParams{ProjectID: snap.ProjectID, Sha256: snap.BodySha256})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		ct := ""
		for _, h := range headers {
			if h[0] == "Content-Type" {
				ct = h[1]
			}
		}
		data["Raw"] = seo.Decode(ct, content)
	}

	data["Prev"], err = s.neighbour(r, snap, true)
	if err == nil {
		data["Next"], err = s.neighbour(r, snap, false)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}

	crumbs := append(projectCrumbs(dbgen.Project{ID: snap.ProjectID, Domain: snap.Domain}, true),
		crumb{Label: fmt.Sprintf("Check #%d", snap.CheckID), Href: fmt.Sprintf("/c/%d", snap.CheckID)},
		crumb{Label: fmt.Sprintf("Snapshot #%d", snap.ID)})
	s.render(w, r, "snapshot", fmt.Sprintf("Snapshot #%d", snap.ID), crumbs, data)
}

// neighbour returns the id of the Page's previous or next Snapshot, 0 if none.
func (s *server) neighbour(r *http.Request, snap dbgen.GetSnapshotViewRow, prev bool) (int64, error) {
	var (
		id  int64
		err error
	)
	at := snap.FetchedAt
	if prev {
		id, err = s.q.PreviousSnapshotID(r.Context(), dbgen.PreviousSnapshotIDParams{PageID: snap.PageID, FetchedAt: at, ID: snap.ID})
	} else {
		id, err = s.q.NextSnapshotID(r.Context(), dbgen.NextSnapshotIDParams{PageID: snap.PageID, FetchedAt: at, ID: snap.ID})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
