package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/ihorbryk/seo-site-checker/internal/links"
	"github.com/ihorbryk/seo-site-checker/internal/redirect"
	"github.com/ihorbryk/seo-site-checker/internal/seo"
	"github.com/ihorbryk/seo-site-checker/internal/urlnorm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// storeResult records a Fetch's result on the lane's transaction: a skip, a
// failure, or a Snapshot with its body, Diff and outgoing links.
func storeResult(ctx context.Context, q *dbgen.Queries, chk dbgen.Check, f *dbgen.GetClaimedFetchRow, res fetchResult) error {
	if res.SkipReason != "" {
		return q.FinishFetchSkipped(ctx, dbgen.FinishFetchSkippedParams{
			ID: f.ID, SkipReason: pgtype.Text{String: res.SkipReason, Valid: true},
		})
	}
	if res.FailKind != "" {
		return q.FinishFetchFailure(ctx, dbgen.FinishFetchFailureParams{
			ID:          f.ID,
			FailureKind: pgtype.Text{String: res.FailKind, Valid: true},
			Message:     pgtype.Text{String: res.FailMsg, Valid: true},
			DurationMs:  durationMs(res),
		})
	}
	host := urlnorm.Host(f.Url)
	target := redirect.Classify(res.Status, res.Headers, f.Url, host)
	if err := storeSnapshot(ctx, q, chk, f, res, target); err != nil {
		return err
	}
	found, ok, err := outgoingLinks(res, target, f.Url, host)
	if err != nil || !ok {
		return err
	}
	return discover(ctx, q, chk, f, found)
}

func durationMs(res fetchResult) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(res.Duration.Milliseconds()), Valid: true}
}

// storeSnapshot stores the response's body, Snapshot and Diff against the
// Page's previous Snapshot, and marks the Fetch done. A 3xx body is never read
// by fetch, so a redirect Snapshot has none; its redirect target is its
// content.
func storeSnapshot(ctx context.Context, q *dbgen.Queries, chk dbgen.Check, f *dbgen.GetClaimedFetchRow, res fetchResult, target redirect.Target) error {
	prev, err := q.LatestSnapshotOfPage(ctx, f.PageID)
	hasPrev := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var sum []byte
	if res.HasBody {
		h := sha256.Sum256(res.Body)
		sum = h[:]
		err := q.InsertBody(ctx, dbgen.InsertBodyParams{
			ProjectID: f.ProjectID, Sha256: sum, Content: res.Body, Size: int32(len(res.Body)),
		})
		if err != nil {
			return err
		}
	}
	headers, err := json.Marshal(res.Headers)
	if err != nil {
		return err
	}
	fields := seo.Extract(seo.Input{
		Status: res.Status, Headers: res.Headers, Body: res.Body, HasBody: res.HasBody,
		RedirectTarget: target.URL,
	})
	seoFields, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	err = q.InsertSnapshot(ctx, dbgen.InsertSnapshotParams{
		ID: f.ID, PageID: f.PageID, CheckID: chk.ID, FetchedAt: time.Now(),
		Status: int32(res.Status), Headers: headers, BodySha256: sum, SeoFields: seoFields,
	})
	if err != nil {
		return err
	}
	if hasPrev {
		current := diff.Side{Headers: res.Headers, SEO: fields, BodySHA256: sum, LoadBody: func() ([]byte, error) { return res.Body, nil }}
		if err := storeDiff(ctx, q, chk, f, prev, current); err != nil {
			return err
		}
	}
	return q.FinishFetchSnapshot(ctx, dbgen.FinishFetchSnapshotParams{ID: f.ID, DurationMs: durationMs(res)})
}

// outgoingLinks returns a Snapshot's outgoing links: those of a 2xx HTML
// page, or the single redirect target of an on-domain 3xx. ok is false when
// the Snapshot has none to discover from.
func outgoingLinks(res fetchResult, target redirect.Target, pageURL, host string) (found links.Result, ok bool, err error) {
	switch {
	case res.HasBody && res.Status >= 200 && res.Status < 300:
		found, err = links.Extract(res.Body, pageURL, host)
		return found, err == nil, err
	case target.Kind == redirect.OnDomain:
		if reason := urlnorm.Violation(target.URL); reason != "" {
			found.Dropped = []links.Dropped{{URL: target.URL, Reason: reason}}
		} else {
			found.Links = []links.Link{{URL: target.URL, Source: links.SourceRedirect}}
		}
		return found, true, nil
	}
	return found, false, nil
}

// storeDiff writes the Diff summary of a new Snapshot against the Page's
// previous one, under the Noise rules the Check froze when it started.
func storeDiff(ctx context.Context, q *dbgen.Queries, chk dbgen.Check, f *dbgen.GetClaimedFetchRow, prev dbgen.LatestSnapshotOfPageRow, current diff.Side) error {
	rules, err := diff.ParseRules(chk.NoiseRules)
	if err != nil {
		return fmt.Errorf("check noise rules: %w", err)
	}
	var prevHeaders [][2]string
	if err := json.Unmarshal(prev.Headers, &prevHeaders); err != nil {
		return fmt.Errorf("baseline headers: %w", err)
	}
	var prevSEO seo.Fields
	if err := json.Unmarshal(prev.SeoFields, &prevSEO); err != nil {
		return fmt.Errorf("baseline seo fields: %w", err)
	}
	baseline := diff.Side{
		Headers: prevHeaders, SEO: prevSEO, BodySHA256: prev.BodySha256,
		LoadBody: func() ([]byte, error) {
			return q.GetBodyContent(ctx, dbgen.GetBodyContentParams{ProjectID: f.ProjectID, Sha256: prev.BodySha256})
		},
	}
	sum, err := diff.Summarize(baseline, current, rules)
	if err != nil {
		return err
	}
	changes, err := json.Marshal(sum.SEOChanges)
	if err != nil {
		return err
	}
	return q.InsertDiff(ctx, dbgen.InsertDiffParams{
		SnapshotID: f.ID, BaselineID: prev.ID,
		StatusChanged: sum.StatusChanged, HeadersChanged: sum.HeadersChanged,
		SeoChanged: sum.SEOChanged, BodyChanged: sum.BodyChanged,
		SeoChanges: changes, Tier: string(sum.Tier),
	})
}
