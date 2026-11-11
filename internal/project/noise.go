package project

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/jackc/pgx/v5"
)

// NoiseRules are a Project's own Noise rules as the form edits them: header
// names to ignore and RE2 body masks, in the order entered.
type NoiseRules struct {
	HeaderNames []string
	BodyMasks   []string
}

// ParseNoiseRules validates the Noise rules form: header_names and body_masks
// are textareas with one entry per line. Masks must compile as RE2. Errors are
// keyed "header_names" / "body_masks".
func ParseNoiseRules(f url.Values) (NoiseRules, FieldErrors) {
	errs := FieldErrors{}
	var n NoiseRules
	for _, line := range strings.Split(f.Get("header_names"), "\n") {
		name := strings.TrimSpace(line)
		switch {
		case name == "":
		case !validHeaderName(name):
			errs["header_names"] = append(errs["header_names"], fmt.Sprintf("%q is not a valid header name.", name))
		case !containsFold(n.HeaderNames, name):
			n.HeaderNames = append(n.HeaderNames, name)
		}
	}
	for _, line := range strings.Split(f.Get("body_masks"), "\n") {
		pat := strings.TrimRight(line, "\r")
		if strings.TrimSpace(pat) == "" {
			continue
		}
		if _, err := regexp.Compile(pat); err != nil {
			errs["body_masks"] = append(errs["body_masks"], fmt.Sprintf("Body mask %q is not valid RE2: %v.", pat, err))
			continue
		}
		dup := false
		for _, m := range n.BodyMasks {
			dup = dup || m == pat
		}
		if !dup {
			n.BodyMasks = append(n.BodyMasks, pat)
		}
	}
	if len(errs) == 0 {
		return n, nil
	}
	return n, errs
}

func containsFold(xs []string, s string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Rules converts to diff rules, headers first.
func (n NoiseRules) Rules() diff.Rules {
	var rs diff.Rules
	for _, h := range n.HeaderNames {
		rs = append(rs, diff.Rule{Kind: diff.KindHeader, Value: h})
	}
	for _, m := range n.BodyMasks {
		rs = append(rs, diff.Rule{Kind: diff.KindBodyMask, Value: m})
	}
	return rs
}

// LoadNoiseRules reads a Project's own Noise rules.
func LoadNoiseRules(ctx context.Context, q *dbgen.Queries, projectID int64) (NoiseRules, error) {
	rows, err := q.ListNoiseRules(ctx, projectID)
	var n NoiseRules
	for _, r := range rows {
		switch r.Kind {
		case diff.KindHeader:
			n.HeaderNames = append(n.HeaderNames, r.Value)
		case diff.KindBodyMask:
			n.BodyMasks = append(n.BodyMasks, r.Value)
		}
	}
	return n, err
}

// SaveNoiseRules replaces a Project's Noise rules. A Check that already started
// keeps the rules it froze; the next Check uses these.
func SaveNoiseRules(ctx context.Context, db interface {
	Begin(context.Context) (pgx.Tx, error)
}, projectID int64, n NoiseRules) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := dbgen.New(tx)
	if err := q.DeleteNoiseRules(ctx, projectID); err != nil {
		return err
	}
	for i, r := range n.Rules() {
		if err := q.InsertNoiseRule(ctx, dbgen.InsertNoiseRuleParams{ProjectID: projectID, Kind: r.Kind, Value: r.Value, Position: int32(i)}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
