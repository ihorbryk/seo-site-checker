package web

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

// Each SQL filter (and its count) must hold exactly the Pages poolState puts
// under that filter: Pages in every combination of state, origin, orphan
// streak and pattern match.
func TestSQLFiltersAgreeWithPoolStateForEveryCombination(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := dbgen.New(pool)
	p := testfixtures.Project(t, pool)
	patterns := []string{"/hit/**"}

	want := map[string][]int64{} // filter key -> Page ids, from poolState
	n := 0
	for _, state := range []string{page.StateActive, page.StateRetired, page.StateIgnored} {
		for _, origin := range []string{page.OriginSeed, page.OriginDiscovered, page.OriginManual} {
			for _, streak := range []int32{0, 2} {
				for _, match := range []bool{false, true} {
					n++
					path := fmt.Sprintf("/%d", n)
					if match {
						path = fmt.Sprintf("/hit/%d", n)
					}
					url := "https://" + p.Domain + path
					excluded := page.Excluded(patterns, origin, url)
					var id int64
					if err := pool.QueryRow(ctx, `INSERT INTO pages (project_id, url, state, origin, orphan_streak, excluded)
						VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
						p.ID, url, state, origin, streak, excluded).Scan(&id); err != nil {
						t.Fatal(err)
					}
					key, _ := poolState(pagePoolState{State: state, OrphanStreak: streak, Excluded: excluded})
					want[key] = append(want[key], id)
					want[""] = append(want[""], id)
					if key == "orphaned" {
						want[page.StateActive] = append(want[page.StateActive], id)
					}
				}
			}
		}
	}

	counts, err := q.CountPagePool(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range poolFilters {
		got, err := f.list(ctx, q, p.ID, window{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		w := want[f.Key]
		slices.Sort(got)
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("filter %q lists %v, poolState puts %v there", f.Key, got, w)
		}
		if c := f.count(counts); c != int64(len(w)) {
			t.Errorf("filter %q counts %d, want %d", f.Key, c, len(w))
		}
	}
	for _, key := range []string{"excluded", "orphaned", page.StateRetired, page.StateIgnored} {
		if len(want[key]) == 0 {
			t.Errorf("no Page falls under %q: the combinations are not exercising it", key)
		}
	}
}
