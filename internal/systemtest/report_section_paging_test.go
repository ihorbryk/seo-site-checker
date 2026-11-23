package systemtest_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

// A Report section is read one page at a time, whatever its size (#78). These
// tests build two ended Checks over the same 1,200 Pages, one whose sections
// are dense (hundreds of rows) and one whose sections are sparse (a handful),
// and compare every page of every section with a plain reference list.

const pagingPages = 1200

func pagingURL(n int) string { return fmt.Sprintf("https://shop.test/p%04d", n) }

// pagingCheck is what one Check of the paging fixture holds, as Go rules over
// the Page number n, and so also the reference lists.
type pagingCheck struct {
	id       int64
	outcome  func(n int) string // snapshot, failure or skipped
	tier     func(n int) string // of a snapshot: critical, seo, headers, body or empty
	event    func(n int) string // new, vanished, retired or ""
	expected map[string][]string
}

var pagingTiers = []string{"critical", "seo", "headers", "body"}

// reference derives each section's URLs in Report order from the rules.
func (c *pagingCheck) reference() {
	c.expected = map[string][]string{}
	byTier := map[string][]string{}
	for n := 1; n <= pagingPages; n++ {
		u := pagingURL(n)
		switch c.outcome(n) {
		case "failure":
			c.expected["failures"] = append(c.expected["failures"], u)
		case "skipped":
			c.expected["skipped-list"] = append(c.expected["skipped-list"], u)
		default:
			if t := c.tier(n); t != "empty" {
				byTier[t] = append(byTier[t], u)
			}
		}
		if e := c.event(n); e != "" {
			c.expected[e] = append(c.expected[e], u)
		}
	}
	for _, t := range pagingTiers {
		c.expected["changed"] = append(c.expected["changed"], byTier[t]...)
	}
}

func pagingFixture(t *testing.T, s *systemtest.System) (denseCheck, sparseCheck *pagingCheck) {
	t.Helper()
	p := s.AddProject("shop.test")
	s.Exec(`INSERT INTO pages (project_id, url, origin, state)
		SELECT $1, 'https://shop.test/p' || lpad(n::text, 4, '0'), 'discovered', 'active'
		FROM generate_series(1, $2::int) AS n`, p.ID, pagingPages)

	dense := &pagingCheck{
		outcome: func(n int) string {
			switch n % 6 {
			case 0:
				return "failure"
			case 1:
				return "skipped"
			}
			return "snapshot"
		},
		tier: func(n int) string { return []string{"critical", "seo", "headers", "body", "empty"}[n%5] },
		event: func(n int) string {
			switch {
			case n%2 == 0:
				return "new"
			case n%11 == 3:
				return "vanished"
			case n%13 == 5:
				return "retired"
			}
			return ""
		},
	}
	sparse := &pagingCheck{
		outcome: func(n int) string {
			switch {
			case n%400 == 7:
				return "failure"
			case n%300 == 4:
				return "skipped"
			}
			return "snapshot"
		},
		tier: func(n int) string {
			if tier, ok := map[int]string{10: "critical", 11: "critical", 600: "seo", 601: "headers", 1000: "body"}[n]; ok {
				return tier
			}
			return "empty"
		},
		event: func(n int) string {
			return map[int]string{2: "new", 900: "new", 3: "vanished", 1199: "retired"}[n]
		},
	}
	for _, c := range []*pagingCheck{dense, sparse} {
		c.id = testfixtures.Check(t, s.Pool, p, testfixtures.WithCheckState(check.StateFinished)).ID
		var ns, eventNs []int32
		var outcomes, tiers, eventKinds []string
		counts := map[string]int{}
		for n := 1; n <= pagingPages; n++ {
			o := c.outcome(n)
			ns, outcomes = append(ns, int32(n)), append(outcomes, o)
			tier := ""
			if o == "snapshot" {
				tier = c.tier(n)
				counts["d_"+tier]++
			}
			counts[o]++
			tiers = append(tiers, tier)
			if e := c.event(n); e != "" {
				eventNs, eventKinds = append(eventNs, int32(n)), append(eventKinds, e)
				counts[e]++
			}
		}
		s.Exec(`INSERT INTO fetches (check_id, page_id, state, outcome, failure_kind, skip_reason)
			SELECT $1, p.id, 'done', o.outcome, CASE WHEN o.outcome = 'failure' THEN 'timeout' END,
				CASE WHEN o.outcome = 'skipped' THEN 'robots_unreachable' END
			FROM unnest($2::int[], $3::text[]) AS o(n, outcome)
			JOIN pages p ON p.project_id = $4 AND p.url = 'https://shop.test/p' || lpad(o.n::text, 4, '0')`,
			c.id, ns, outcomes, p.ID)
		s.Exec(`INSERT INTO snapshots (id, page_id, check_id, fetched_at, status, headers, seo_fields)
			SELECT f.id, f.page_id, f.check_id, now(), 200, '[]', '{}'
			FROM fetches f WHERE f.check_id = $1 AND f.outcome = 'snapshot'`, c.id)
		s.Exec(`INSERT INTO diffs (snapshot_id, check_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, tier)
			SELECT f.id, f.check_id, f.id, false, false, false, t.tier <> 'empty', t.tier
			FROM unnest($2::int[], $3::text[]) AS t(n, tier)
			JOIN pages p ON p.project_id = $4 AND p.url = 'https://shop.test/p' || lpad(t.n::text, 4, '0')
			JOIN fetches f ON f.check_id = $1 AND f.page_id = p.id
			WHERE t.tier <> ''`, c.id, ns, tiers, p.ID)
		s.Exec(`INSERT INTO page_events (check_id, page_id, kind)
			SELECT $1, p.id, e.kind
			FROM unnest($2::int[], $3::text[]) AS e(n, kind)
			JOIN pages p ON p.project_id = $4 AND p.url = 'https://shop.test/p' || lpad(e.n::text, 4, '0')`,
			c.id, eventNs, eventKinds, p.ID)
		s.Exec(`UPDATE checks SET count_snapshots = $2, count_failures = $3, count_skipped = $4,
			count_new = $5, count_vanished = $6, count_retired = $7,
			count_diff_critical = $8, count_diff_seo = $9, count_diff_headers = $10, count_diff_body = $11, count_diff_empty = $12
			WHERE id = $1`, c.id, counts["snapshot"], counts["failure"], counts["skipped"],
			counts["new"], counts["vanished"], counts["retired"],
			counts["d_critical"], counts["d_seo"], counts["d_headers"], counts["d_body"], counts["d_empty"])
		c.reference()
	}
	return dense, sparse
}

var pagingSections = []string{"changed", "new", "vanished", "retired", "failures", "skipped-list"}

func TestEveryPageOfEveryReportSectionMatchesASortedReferenceListDenseAndSparse(t *testing.T) {
	s := systemtest.New(t)
	dense, sparse := pagingFixture(t, s)
	for name, c := range map[string]*pagingCheck{"dense": dense, "sparse": sparse} {
		path := s.CheckPath(c.id)
		for _, table := range pagingSections {
			id := table
			if id == "skipped-list" {
				id = "skipped"
			}
			want := c.expected[table]
			if name == "dense" && len(want) < 12 || name == "sparse" && len(want) > 10 {
				t.Fatalf("%s %s: %d rows is not a %s section", name, table, len(want), name)
			}
			for pg := 1; pg <= max(1, (len(want)+49)/50); pg++ {
				d := s.Page(s.Get(fmt.Sprintf("%s?show=skipped&%s_page=%d", path, id, pg)))
				lo, hi := min((pg-1)*50, len(want)), min(pg*50, len(want))
				if got := cells(d, "#"+table); !slices.Equal(got, want[lo:hi]) {
					t.Errorf("%s %s page %d = %v, want %v", name, table, pg, got, want[lo:hi])
				}
			}
		}
	}
}

func TestAReportRequestRunsNoWholeSectionQuery(t *testing.T) {
	s := systemtest.New(t)
	dense, _ := pagingFixture(t, s)
	queries := s.WebQueries(func() { s.Get(s.CheckPath(dense.id) + "?show=skipped") })
	for _, q := range queries {
		switch q {
		case "CountFetchFailures", "CountSkippedFetches", "CountPageEvents", "ListCheckDiffStates":
			t.Errorf("Report request ran %s (all: %v)", q, queries)
		}
	}
}

// Both ways of reading a section are right on both shapes of section; the web
// app chooses between them by the stored counts.
func TestBothSectionReadsReturnTheSameRowsInTheSameOrder(t *testing.T) {
	s := systemtest.New(t)
	dense, sparse := pagingFixture(t, s)
	ctx := t.Context()
	var projectID int64
	if err := s.Pool.QueryRow(ctx, `SELECT project_id FROM checks WHERE id = $1`, dense.id).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	urlOf := map[int64]string{}
	rows, err := s.Pool.Query(ctx, `SELECT s.id, p.url FROM snapshots s JOIN pages p ON p.id = s.page_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var u string
		if err := rows.Scan(&id, &u); err != nil {
			t.Fatal(err)
		}
		urlOf[id] = u
	}
	rows.Close()

	for name, c := range map[string]*pagingCheck{"dense": dense, "sparse": sparse} {
		for _, w := range [][2]int32{{0, 100000}, {37, 50}, {0, 1}} {
			off, lim := w[0], w[1]
			slice := func(all []string) []string { return all[min(int(off), len(all)):min(int(off+lim), len(all))] }

			fd, err := s.Q.ListFetchFailuresDense(ctx, dbgen.ListFetchFailuresDenseParams{CheckID: c.id, ProjectID: projectID, RowLimit: lim, RowOffset: off})
			if err != nil {
				t.Fatal(err)
			}
			fs, err := s.Q.ListFetchFailuresSparse(ctx, dbgen.ListFetchFailuresSparseParams{CheckID: c.id, RowLimit: lim, RowOffset: off})
			if err != nil {
				t.Fatal(err)
			}
			var gd, gs []string
			for _, r := range fd {
				gd = append(gd, r.Url)
			}
			for _, r := range fs {
				gs = append(gs, r.Url)
			}
			check3(t, name+" failures", gd, gs, slice(c.expected["failures"]))

			sd, err := s.Q.ListSkippedFetchesDense(ctx, dbgen.ListSkippedFetchesDenseParams{CheckID: c.id, ProjectID: projectID, RowLimit: lim, RowOffset: off})
			if err != nil {
				t.Fatal(err)
			}
			ss, err := s.Q.ListSkippedFetchesSparse(ctx, dbgen.ListSkippedFetchesSparseParams{CheckID: c.id, RowLimit: lim, RowOffset: off})
			if err != nil {
				t.Fatal(err)
			}
			gd, gs = nil, nil
			for _, r := range sd {
				gd = append(gd, r.Url)
			}
			for _, r := range ss {
				gs = append(gs, r.Url)
			}
			check3(t, name+" skipped", gd, gs, slice(c.expected["skipped-list"]))

			for _, kind := range []string{"new", "vanished", "retired"} {
				ed, err := s.Q.ListPageEventsDense(ctx, dbgen.ListPageEventsDenseParams{CheckID: c.id, ProjectID: projectID, Kinds: []string{kind}, RowLimit: lim, RowOffset: off})
				if err != nil {
					t.Fatal(err)
				}
				es, err := s.Q.ListPageEventsSparse(ctx, dbgen.ListPageEventsSparseParams{CheckID: c.id, Kinds: []string{kind}, RowLimit: lim, RowOffset: off})
				if err != nil {
					t.Fatal(err)
				}
				gd, gs = nil, nil
				for _, r := range ed {
					gd = append(gd, r.Url)
				}
				for _, r := range es {
					gs = append(gs, r.Url)
				}
				check3(t, name+" "+kind, gd, gs, slice(c.expected[kind]))
			}

			for _, tier := range pagingTiers {
				var want []string
				for n := 1; n <= pagingPages; n++ {
					if c.outcome(n) == "snapshot" && c.tier(n) == tier {
						want = append(want, pagingURL(n))
					}
				}
				td, err := s.Q.ListChangedSnapshotIDsDense(ctx, dbgen.ListChangedSnapshotIDsDenseParams{CheckID: c.id, ProjectID: projectID, Tier: tier, RowLimit: lim, RowOffset: off})
				if err != nil {
					t.Fatal(err)
				}
				ts, err := s.Q.ListChangedSnapshotIDsSparse(ctx, dbgen.ListChangedSnapshotIDsSparseParams{CheckID: c.id, Tier: tier, RowLimit: lim, RowOffset: off})
				if err != nil {
					t.Fatal(err)
				}
				gd, gs = nil, nil
				for _, id := range td {
					gd = append(gd, urlOf[id])
				}
				for _, id := range ts {
					gs = append(gs, urlOf[id])
				}
				check3(t, name+" "+tier, gd, gs, slice(want))
			}
		}
	}
}

func check3(t *testing.T, what string, dense, sparse, want []string) {
	t.Helper()
	if !slices.Equal(dense, want) {
		t.Errorf("%s dense read = %d rows %v, want %d rows %v", what, len(dense), head(dense), len(want), head(want))
	}
	if !slices.Equal(sparse, want) {
		t.Errorf("%s sparse read = %d rows %v, want %d rows %v", what, len(sparse), head(sparse), len(want), head(want))
	}
}

func head(s []string) []string { return s[:min(len(s), 3)] }
