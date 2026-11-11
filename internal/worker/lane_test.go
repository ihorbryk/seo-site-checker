package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
)

// poolTx lets rivertest.Worker run against the pool: the lane opens its own
// transactions, so the job row must not be locked by a test transaction.
type poolTx struct {
	pgx.Tx
	pool *pgxpool.Pool
	conn *pgx.Conn
}

func (p poolTx) Conn() *pgx.Conn { return p.conn }

// newPoolTx holds one pooled connection for the test's lifetime, only for its
// settings.
func newPoolTx(t testing.TB, pool *pgxpool.Pool) poolTx {
	c, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Release)
	return poolTx{pool: pool, conn: c.Conn()}
}

func (p poolTx) Begin(ctx context.Context) (pgx.Tx, error) { return p.pool.Begin(ctx) }
func (p poolTx) Commit(context.Context) error              { return nil }
func (p poolTx) Rollback(context.Context) error            { return nil }
func (p poolTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}
func (p poolTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}
func (p poolTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}
func (p poolTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return p.pool.SendBatch(ctx, b)
}
func (p poolTx) CopyFrom(ctx context.Context, t pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error) {
	return p.pool.CopyFrom(ctx, t, cols, src)
}

// rig is a Check started through the real StartCheck worker, with its lane
// job waiting to be worked.
type rig struct {
	t       *testing.T
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	checkID int64
	tx      poolTx
	lane    *rivertype.JobRow
	laneW   *rivertest.Worker[check.LaneArgs, pgx.Tx]
	finW    *rivertest.Worker[check.FinalizeCheckArgs, pgx.Tx]
}

func newRig(t *testing.T, client *http.Client, pages ...string) *rig {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	p := testfixtures.Project(t, pool, testfixtures.WithDomain("shop.test"))
	for i, path := range pages {
		origin := "discovered"
		if i == 0 {
			origin = "seed"
		}
		testfixtures.Page(t, pool, p, path, testfixtures.WithOrigin(origin))
	}
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	c, err := check.Start(ctx, pool, rc, p.ID, check.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &deps{pool: pool, q: dbgen.New(pool), client: client, log: log}
	driver := riverpgxv5.New(pool)
	cfg := func() *river.Config { return &river.Config{Logger: log} }
	r := &rig{tx: newPoolTx(t, pool),
		t: t, pool: pool, q: d.q, checkID: c.ID,
		laneW: rivertest.NewWorker[check.LaneArgs, pgx.Tx](t, driver, cfg(), &laneWorker{deps: d}),
		finW:  rivertest.NewWorker[check.FinalizeCheckArgs, pgx.Tx](t, driver, cfg(), &finalizeWorker{deps: d}),
	}
	startW := rivertest.NewWorker[check.StartCheckArgs, pgx.Tx](t, driver, cfg(), &startWorker{deps: d})
	if _, err := startW.Work(ctx, t, r.tx, check.StartCheckArgs{CheckID: c.ID}, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT id FROM river_job WHERE kind = 'lane' AND (args->>'check_id')::bigint = $1`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(ids) != 1 {
		t.Fatalf("lane jobs %v, err %v", ids, err)
	}
	r.lane, err = rc.JobGet(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) setAttempt(n int) {
	r.t.Helper()
	if _, err := r.pool.Exec(context.Background(), `UPDATE river_job SET attempt = $2 WHERE id = $1`, r.lane.ID, n); err != nil {
		r.t.Fatal(err)
	}
	r.lane.Attempt = n
}

func (r *rig) workLane() (*rivertest.WorkResult, error) {
	r.t.Helper()
	return r.laneW.WorkJob(context.Background(), r.t, r.tx, r.lane)
}

func (r *rig) finalize() {
	r.t.Helper()
	if _, err := r.finW.Work(context.Background(), r.t, r.tx, check.FinalizeCheckArgs{CheckID: r.checkID}, nil); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) fetch() dbgen.Fetch {
	r.t.Helper()
	var f dbgen.Fetch
	err := r.pool.QueryRow(context.Background(),
		`SELECT id, check_id, page_id, state, job_id, outcome, skip_reason, failure_kind, message, started_at, duration_ms
		 FROM fetches WHERE check_id = $1`, r.checkID).
		Scan(&f.ID, &f.CheckID, &f.PageID, &f.State, &f.JobID, &f.Outcome, &f.SkipReason, &f.FailureKind, &f.Message, &f.StartedAt, &f.DurationMs)
	if err != nil {
		r.t.Fatal(err)
	}
	return f
}

func (r *rig) checkRow() dbgen.Check {
	r.t.Helper()
	c, err := r.q.GetCheck(context.Background(), r.checkID)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func siteClient(t *testing.T) *http.Client {
	site := fakesite.New(t, "shop.test")
	site.Set("/", fakesite.Response{Body: "<html></html>"})
	n := fakesite.NewNetwork()
	n.Add(site)
	return n.Client()
}

// A worker that died mid-Fetch leaves the Fetch in flight under the lane's job
// id; River retries the same job, which runs the Fetch again from scratch.
func TestRetriedLaneRerunsItsInFlightFetchAndTheCheckFinishes(t *testing.T) {
	r := newRig(t, siteClient(t), "/")
	if f := r.fetch(); f.State != "in_flight" || f.JobID.Int64 != r.lane.ID {
		t.Fatalf("setup: fetch %+v is not claimed by lane %d", f, r.lane.ID)
	}
	// The first attempt started (and its worker died) before this retry.
	r.setAttempt(1)

	res, err := r.workLane()
	if err != nil || res.EventKind != river.EventKindJobCompleted {
		t.Fatalf("lane: %v, %v", res, err)
	}
	if f := r.fetch(); f.State != "done" || f.Outcome.String != "snapshot" {
		t.Fatalf("fetch after retry: %+v", f)
	}
	r.finalize()
	if c := r.checkRow(); c.State != check.StateFinished || c.CountSnapshots != 1 {
		t.Fatalf("check: %+v", c)
	}
}

func (r *rig) breakSettings() {
	r.t.Helper()
	// Settings the lane cannot decode: an error of our own, not the site's.
	if _, err := r.pool.Exec(context.Background(), `UPDATE checks SET settings = '{"timeout_s": "slow"}' WHERE id = $1`, r.checkID); err != nil {
		r.t.Fatal(err)
	}
}

// Our own errors are job errors so River retries the lane, until the last
// attempt, which records an internal failure and lets the Check finish.
func TestInternalErrorRetriesThenRecordsAnInternalFailureOnTheLastAttempt(t *testing.T) {
	r := newRig(t, siteClient(t), "/")
	r.breakSettings()

	res, err := r.workLane()
	if err == nil || res.EventKind != river.EventKindJobFailed {
		t.Fatalf("first attempt: %v, %v; want a job error", res, err)
	}
	if f := r.fetch(); f.State != "in_flight" {
		t.Fatalf("fetch after a retryable error: %+v", f)
	}

	r.lane = res.Job
	r.setAttempt(r.lane.MaxAttempts - 1)
	res, err = r.workLane()
	if err != nil || res.EventKind != river.EventKindJobCompleted {
		t.Fatalf("last attempt: %v, %v", res, err)
	}
	f := r.fetch()
	if f.State != "done" || f.Outcome.String != "failure" || f.FailureKind.String != FailInternal || f.Message.String == "" {
		t.Fatalf("fetch after the last attempt: %+v", f)
	}
	r.finalize()
	if c := r.checkRow(); c.State != check.StateFinished || c.CountFailures != 1 {
		t.Fatalf("check: %+v", c)
	}
}

type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) { panic("boom") }

// A panic is one of our own errors too.
func TestPanicOnTheLastAttemptIsAnInternalFailure(t *testing.T) {
	r := newRig(t, &http.Client{Transport: panicTransport{}}, "/")
	r.setAttempt(r.lane.MaxAttempts - 1)

	res, err := r.workLane()
	if err != nil || res.EventKind != river.EventKindJobCompleted {
		t.Fatalf("lane: %v, %v", res, err)
	}
	if f := r.fetch(); f.FailureKind.String != FailInternal {
		t.Fatalf("fetch: %+v", f)
	}
	r.finalize()
	if c := r.checkRow(); c.State != check.StateFinished {
		t.Fatalf("check: %+v", c)
	}
}

func TestPanicBeforeTheLastAttemptIsAJobError(t *testing.T) {
	r := newRig(t, &http.Client{Transport: panicTransport{}}, "/")
	_, err := r.workLane()
	if err == nil || !strings.Contains(err.Error(), "panic: boom") {
		t.Fatalf("err = %v, want a panic error", err)
	}
}

func TestLaneTimeoutIsTheFetchTimeoutPlus30Seconds(t *testing.T) {
	w := &laneWorker{}
	got := w.Timeout(&river.Job[check.LaneArgs]{Args: check.LaneArgs{FetchTimeoutS: 20}})
	if got != 50*time.Second {
		t.Fatalf("timeout = %s", got)
	}
}
