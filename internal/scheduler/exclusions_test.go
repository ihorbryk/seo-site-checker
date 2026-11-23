package scheduler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/scheduler"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

func (e *env) restartExclusions() int {
	e.t.Helper()
	n, err := scheduler.RestartExclusions(context.Background(), e.pool, e.rc)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) excludeJobs(states ...string) (n int) {
	e.t.Helper()
	err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM river_job WHERE kind = 'exclude_recompute' AND state::text = ANY($1)`, states).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// A recompute the worker never finished (its job was discarded, or an old
// worker that doesn't know the kind failed it, ADR 0009) is queued again; one
// that has a live job, or is finished, is left alone.
func TestAnUnfinishedExcludeRecomputeIsQueuedAgainOnlyWhenNoJobIsLive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := testfixtures.Project(t, e.pool)
	if _, err := e.pool.Exec(ctx,
		`UPDATE projects SET exclude_patterns = '{"/x"}', exclude_gen = 3, exclude_cursor = 42 WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.restartExclusions(); n != 1 {
		t.Fatalf("restarted %d, want 1", n)
	}
	var args string
	if err := e.pool.QueryRow(ctx, `SELECT args::text FROM river_job WHERE kind = 'exclude_recompute'`).Scan(&args); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"generation": 3`, `"after_page_id": 42`} {
		if !strings.Contains(args, want) {
			t.Errorf("job args %s lack %s", args, want)
		}
	}
	if n := e.restartExclusions(); n != 0 || e.excludeJobs("available") != 1 {
		t.Fatalf("a second pass restarted %d with %d jobs, want 0 and 1", n, e.excludeJobs("available"))
	}
	if _, err := e.pool.Exec(ctx, `UPDATE river_job SET state = 'discarded', finalized_at = now() WHERE kind = 'exclude_recompute'`); err != nil {
		t.Fatal(err)
	}
	if n := e.restartExclusions(); n != 1 {
		t.Fatalf("after the job was discarded restarted %d, want 1", n)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE river_job SET state = 'completed', finalized_at = now() WHERE kind = 'exclude_recompute'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE projects SET exclude_done_gen = 3 WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.restartExclusions(); n != 0 {
		t.Fatalf("a finished recompute was restarted (%d)", n)
	}
}
