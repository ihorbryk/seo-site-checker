package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

// excludeRig is a Project with a seed Page and n Pages /p/1 ... /p/n whose
// ExcludeRecompute jobs the test works one by one.
type excludeRig struct {
	t       *testing.T
	pool    *pgxpool.Pool
	rc      *river.Client[pgx.Tx]
	tx      poolTx
	project dbgen.Project
	w       *rivertest.Worker[check.ExcludeRecomputeArgs, pgx.Tx]
}

func newExcludeRig(t *testing.T, n, batch int) *excludeRig {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	p := testfixtures.Project(t, pool, testfixtures.WithDomain("shop.test"))
	testfixtures.Page(t, pool, p, "/", testfixtures.WithOrigin(page.OriginSeed))
	if _, err := pool.Exec(ctx, `INSERT INTO pages (project_id, url, origin)
		SELECT $1, 'https://shop.test/p/' || g, $3 FROM generate_series(1, $2::int) g ORDER BY g`,
		p.ID, n, page.OriginDiscovered); err != nil {
		t.Fatal(err)
	}
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &deps{pool: pool, q: dbgen.New(pool), log: log, excludeBatchSize: batch}
	return &excludeRig{
		t: t, pool: pool, rc: rc, tx: newPoolTx(t, pool), project: p,
		w: rivertest.NewWorker[check.ExcludeRecomputeArgs, pgx.Tx](t, riverpgxv5.New(pool), &river.Config{Logger: log}, &excludeWorker{deps: d}),
	}
}

func (r *excludeRig) save(patterns ...string) {
	r.t.Helper()
	rs := project.RequestSettings{Schedule: "manual", UserAgent: "Bot/1", TimeoutS: 30, MaxParallel: 2, ExcludePatterns: patterns}
	if err := project.SaveRequestSettings(context.Background(), r.pool, r.rc, r.project.ID, rs); err != nil {
		r.t.Fatal(err)
	}
}

// work works the oldest available job and reports its args, or false when
// none is left.
func (r *excludeRig) work() (check.ExcludeRecomputeArgs, bool) {
	r.t.Helper()
	ctx := context.Background()
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind = $1 AND state = 'available' ORDER BY id LIMIT 1`,
		check.ExcludeRecomputeArgs{}.Kind()).Scan(&id)
	if err != nil {
		return check.ExcludeRecomputeArgs{}, false
	}
	job, err := r.rc.JobGet(ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.w.WorkJob(ctx, r.t, r.tx, job); err != nil {
		r.t.Fatal(err)
	}
	var args check.ExcludeRecomputeArgs
	if err := json.Unmarshal(job.EncodedArgs, &args); err != nil {
		r.t.Fatal(err)
	}
	return args, true
}

func (r *excludeRig) workAll() (jobs int) {
	r.t.Helper()
	for {
		if _, ok := r.work(); !ok {
			return jobs
		}
		jobs++
	}
}

func (r *excludeRig) excluded() []string {
	r.t.Helper()
	rows, err := r.pool.Query(context.Background(), `SELECT url FROM pages WHERE project_id = $1 AND excluded ORDER BY url`, r.project.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

// Each job recomputes one batch and chains the next; the answers end up as the
// patterns say, the seed Page never excluded. Nothing is stored for a Page
// before its job.
func TestExcludeRecomputeHandlesOneBatchPerJob(t *testing.T) {
	r := newExcludeRig(t, 24, 10)
	r.save("/p/1*", "/")
	if got := r.excluded(); len(got) != 0 {
		t.Fatalf("answers changed before any job ran: %q", got)
	}
	// 25 Pages in batches of 10: three jobs.
	if jobs := r.workAll(); jobs != 3 {
		t.Errorf("%d jobs, want 3", jobs)
	}
	want := []string{"https://shop.test/p/1"}
	for _, n := range []string{"10", "11", "12", "13", "14", "15", "16", "17", "18", "19"} {
		want = append(want, "https://shop.test/p/"+n)
	}
	slices.Sort(want)
	if got := r.excluded(); !slices.Equal(got, want) {
		t.Errorf("excluded %q\nwant %q", got, want)
	}
}

// The Project records that a generation's recompute reached its last Page, so
// the scheduler can tell an unfinished one from a finished one.
func TestExcludeRecomputeMarksItsGenerationDoneAfterTheLastBatch(t *testing.T) {
	r := newExcludeRig(t, 19, 10) // 20 Pages: two full batches, then an empty one
	r.save("/p/1*")
	done := func() (gen, doneGen int64) {
		if err := r.pool.QueryRow(context.Background(),
			`SELECT exclude_gen, exclude_done_gen FROM projects WHERE id = $1`, r.project.ID).Scan(&gen, &doneGen); err != nil {
			t.Fatal(err)
		}
		return gen, doneGen
	}
	r.work()
	r.work()
	if gen, d := done(); d == gen {
		t.Fatalf("generation %d marked done before the last batch", gen)
	}
	r.work()
	if gen, d := done(); d != gen {
		t.Errorf("done generation %d, want %d after the last batch", d, gen)
	}
}

// A job River runs twice (a retry after its commit, or the rescuer) changes
// nothing and chains nothing.
func TestAnExcludeRecomputeJobRunTwiceChangesNothing(t *testing.T) {
	r := newExcludeRig(t, 24, 10)
	r.save("/p/1*")
	first, _ := r.work() // batch 1; its successor is queued
	before := r.excluded()
	var jobs int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = $1`, first.Kind()).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	// Run the very same args again, and a made-up cursor, by hand.
	for _, args := range []check.ExcludeRecomputeArgs{first, {ProjectID: first.ProjectID, Generation: first.Generation, AfterPageID: 7}} {
		if _, err := r.w.Work(context.Background(), t, r.tx, args, nil); err != nil {
			t.Fatal(err)
		}
	}
	var after int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = $1`, first.Kind()).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.excluded(), before) || after != jobs+2 { // +2: the two jobs I inserted by working them
		t.Errorf("duplicate changed answers or chained jobs: %d -> %d jobs", jobs, after)
	}
}

// Patterns saved again while a recompute runs: the old chain stops and every
// Page ends with the answer for the newest patterns, including the Pages the
// old chain had already rewritten.
func TestSavingPatternsAgainMidRecomputeEndsWithTheLatestPatterns(t *testing.T) {
	r := newExcludeRig(t, 24, 10)
	r.save("/p/1*")
	r.work() // the old chain rewrote the first batch
	r.save("/p/2*")
	r.workAll()
	want := []string{"https://shop.test/p/2"}
	for _, n := range []string{"20", "21", "22", "23", "24"} {
		want = append(want, "https://shop.test/p/"+n)
	}
	slices.Sort(want)
	if got := r.excluded(); !slices.Equal(got, want) {
		t.Errorf("excluded %q\nwant %q", got, want)
	}
}

// Saving identical patterns starts no recompute.
func TestSavingUnchangedPatternsStartsNoRecompute(t *testing.T) {
	r := newExcludeRig(t, 5, 10)
	r.save("/p/1*")
	r.workAll()
	r.save("/p/1*")
	if _, ok := r.work(); ok {
		t.Error("a recompute was queued for unchanged patterns")
	}
}

func TestExcludeRecomputeTimeoutStaysBelowTheRescueDelay(t *testing.T) {
	if d := (&excludeWorker{}).Timeout(nil); d >= rescueStuckJobsAfter {
		t.Errorf("exclude timeout %s, rescue after %s", d, rescueStuckJobsAfter)
	}
}
