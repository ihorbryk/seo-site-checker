package web

import (
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
)

// A section is read by walking the Project's Pages when it is dense among
// them. A Check that fetched few of a large Project's Pages (the rest retired
// or ignored) must not count them as dense by its Fetches alone: the walk
// steps over every Page (ADR 0010).
func TestASectionIsDenseAmongTheProjectsPagesNotJustTheChecksFetches(t *testing.T) {
	big := dbgen.Check{CountSnapshots: 99_000, CountFailures: 1_000, CountPages: 2_000_000}
	for _, tc := range []struct {
		name  string
		check dbgen.Check
		n     int64
		want  bool
	}{
		{"1,000 of 100,000 Fetches among 2M Pages is sparse", big, 1_000, false},
		{"20,000 of 2M Pages is dense", big, 20_000, true},
		{"a Check ended before count_pages was stored falls back to its Fetches", dbgen.Check{CountSnapshots: 99_000, CountFailures: 1_000}, 1_000, true},
		{"fully fetched Project", dbgen.Check{CountSnapshots: 2_000_000, CountPages: 2_000_000}, 20_000, true},
		{"nothing is dense in an empty Check", dbgen.Check{}, 0, true},
	} {
		rep := &report{check: tc.check}
		if got := rep.dense(tc.n); got != tc.want {
			t.Errorf("%s: dense(%d) = %v, want %v", tc.name, tc.n, got, tc.want)
		}
	}
}
