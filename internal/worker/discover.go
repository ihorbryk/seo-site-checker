package worker

import (
	"context"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/links"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/urlnorm"
	"github.com/jackc/pgx/v5/pgtype"
)

// discover stores a Snapshot's outgoing links (those of a 2xx HTML page, or a
// 3xx's redirect target), turns new URLs into discovered Pages with pending
// Fetches, and records dropped URLs.
func discover(ctx context.Context, q *dbgen.Queries, chk dbgen.Check, f *dbgen.GetClaimedFetchRow, found links.Result) error {
	settings, err := check.ParseSettings(chk.Settings)
	if err != nil {
		return err
	}
	found.Links = withoutExcluded(found.Links, settings.ExcludePatterns, "https://"+urlnorm.Host(f.Url)+"/")
	if len(found.Links) > 0 {
		if err := addLinkedPages(ctx, q, chk, f, found.Links); err != nil {
			return err
		}
	}
	return recordDropped(ctx, q, chk.ID, found.Dropped)
}

// withoutExcluded drops the links the Check's copy of the exclude patterns
// excludes: they are not added to the pool and get no Fetch. The seed Page's
// URL is never excluded.
func withoutExcluded(ls []links.Link, patterns []string, seedURL string) []links.Link {
	kept := ls[:0:0]
	for _, l := range ls {
		origin := page.OriginDiscovered
		if l.URL == seedURL {
			origin = page.OriginSeed
		}
		if !page.Excluded(patterns, origin, l.URL) {
			kept = append(kept, l)
		}
	}
	return kept
}

// addLinkedPages adds the linked URLs to the pool, reactivates retired ones,
// plans Fetches for the active ones and stores the Snapshot's links.
func addLinkedPages(ctx context.Context, q *dbgen.Queries, chk dbgen.Check, f *dbgen.GetClaimedFetchRow, ls []links.Link) error {
	urls := make([]string, 0, len(ls))
	for _, l := range ls {
		urls = append(urls, l.URL)
	}
	if err := q.UpsertDiscoveredPages(ctx, dbgen.UpsertDiscoveredPagesParams{ProjectID: f.ProjectID, Urls: urls}); err != nil {
		return err
	}
	pages, err := q.PageIDsByURL(ctx, dbgen.PageIDsByURLParams{ProjectID: f.ProjectID, Urls: urls})
	if err != nil {
		return err
	}
	idOf := make(map[string]int64, len(pages))
	ids := make([]int64, 0, len(pages))
	for _, p := range pages {
		idOf[p.Url] = p.ID
		ids = append(ids, p.ID)
	}
	if err := reactivate(ctx, q, chk.ID, ids); err != nil {
		return err
	}
	if _, err := q.PlanDiscoveredFetches(ctx, dbgen.PlanDiscoveredFetchesParams{CheckID: chk.ID, PageIds: ids}); err != nil {
		return err
	}
	bySource := map[string][]int64{}
	for _, l := range ls {
		bySource[l.Source] = append(bySource[l.Source], idOf[l.URL])
	}
	for source, pageIDs := range bySource {
		if err := q.InsertLinks(ctx, dbgen.InsertLinksParams{SnapshotID: f.ID, Source: source, PageIds: pageIDs}); err != nil {
			return err
		}
	}
	return nil
}

func recordDropped(ctx context.Context, q *dbgen.Queries, checkID int64, dropped []links.Dropped) error {
	byReason := map[string][]string{}
	for _, d := range dropped {
		byReason[d.Reason] = append(byReason[d.Reason], d.URL)
	}
	for reason, urls := range byReason {
		if err := q.InsertDroppedURLs(ctx, dbgen.InsertDroppedURLsParams{CheckID: checkID, Reason: reason, Urls: urls}); err != nil {
			return err
		}
	}
	return nil
}

// reactivate brings retired Pages that were linked again back into the pool,
// before their Fetches are planned. FinalizeCheck records the events.
func reactivate(ctx context.Context, q *dbgen.Queries, checkID int64, pageIDs []int64) error {
	return q.ReactivateLinkedPages(ctx, dbgen.ReactivateLinkedPagesParams{CheckID: pgtype.Int8{Int64: checkID, Valid: true}, PageIds: pageIDs})
}
