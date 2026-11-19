package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	projectrules "github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

// DefaultPlanBatchSize is how many Pages one StartCheck job plans (ADR 0006).
// A batch costs about 0.3 s on a laptop, so a job stays far below its timeout
// however large the Page pool grows.
const DefaultPlanBatchSize = 20_000

// startTimeout is a StartCheck job's River timeout: room for the robots.txt
// fetch (a Check's fetch timeout is at most 120 s) plus one batch. It must stay
// below rescueStuckJobsAfter, or the rescuer would start a duplicate.
const startTimeout = 3 * time.Minute

type startWorker struct {
	river.WorkerDefaults[check.StartCheckArgs]
	*deps
}

func (w *startWorker) Timeout(*river.Job[check.StartCheckArgs]) time.Duration { return startTimeout }

// begin is what starting a queued Check needs from outside the transaction.
type begin struct {
	settings check.Settings
	robots   *robotsResult
}

// Work plans one batch of a Check's Fetches (ADR 0006). On a queued Check it
// first copies the Request settings and Noise rules onto it and marks it
// planning. Each batch plans one Fetch per active, non-excluded Page among the
// next DefaultPlanBatchSize Pages by id, advances the Check's plan cursor and,
// in the same transaction, enqueues the job for the next batch. The batch that
// reaches the end of the pool starts up to max_parallel lanes, each with a
// Fetch already claimed, and marks the Check running.
//
// It is safe to repeat: a job whose AfterPageID is no longer the Check's plan
// cursor, or whose Check has moved on, does nothing.
func (w *startWorker) Work(ctx context.Context, job *river.Job[check.StartCheckArgs]) error {
	pre, err := w.q.GetCheck(ctx, job.Args.CheckID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the Check is gone; nothing to start
		}
		return err
	}
	var b *begin
	switch pre.State {
	case check.StateQueued:
		// robots.txt is fetched before the transaction, so no row lock is
		// held during the request. A retry fetches it again; nothing is
		// stored until the transaction commits.
		if b, err = w.prepareBegin(ctx, pre); err != nil {
			return err
		}
	case check.StatePlanning, check.StateCancelling:
	default:
		return nil
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := w.q.WithTx(tx)
	rc := river.ClientFromContext[pgx.Tx](ctx)

	c, err := q.LockCheck(ctx, job.Args.CheckID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the Check is gone; nothing to start
		}
		return err
	}
	var settings check.Settings
	switch {
	case c.State == check.StateQueued && b != nil:
		if err := beginPlanning(ctx, q, c, *b); err != nil {
			return err
		}
		settings, c.PlanCursor = b.settings, 0
	case c.State == check.StatePlanning && c.PlanCursor == job.Args.AfterPageID:
		if settings, err = check.ParseSettings(c.Settings); err != nil {
			return err
		}
	case c.State == check.StateCancelling && c.Lanes == 0 && c.PlanCursor == job.Args.AfterPageID:
		// Cancelled while planning: no lane runs, so none will end and
		// finalize it. (FinalizeCheck is unique, so a Check whose last lane
		// already enqueued it is unaffected.)
		if _, err := rc.InsertTx(ctx, tx, check.FinalizeCheckArgs{CheckID: c.ID}, nil); err != nil {
			return err
		}
		return tx.Commit(ctx)
	default:
		return nil // a duplicate or stale job, or a Check that moved on
	}
	if err := w.planBatch(ctx, rc, tx, q, c, settings); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// prepareBegin reads the Project's Request settings and, if they ask for it,
// fetches robots.txt.
func (w *startWorker) prepareBegin(ctx context.Context, c dbgen.Check) (*begin, error) {
	project, err := w.q.GetProject(ctx, c.ProjectID)
	if err != nil {
		return nil, err
	}
	settings, err := check.SettingsOf(project)
	if err != nil {
		return nil, err
	}
	b := &begin{settings: settings}
	if settings.RespectRobots {
		res, err := fetchRobots(ctx, w.client, project.Domain, settings)
		if err != nil {
			return nil, err
		}
		b.robots = &res
	}
	return b, nil
}

// beginPlanning stores robots.txt and freezes the settings and Noise rules
// onto the queued Check c, which becomes planning from the first Page on.
func beginPlanning(ctx context.Context, q *dbgen.Queries, c dbgen.Check, b begin) error {
	rawSettings, err := json.Marshal(b.settings)
	if err != nil {
		return err
	}
	if b.robots != nil {
		if err := storeRobots(ctx, q, c.ID, *b.robots); err != nil {
			return err
		}
	}
	own, err := projectrules.LoadNoiseRules(ctx, q, c.ProjectID)
	if err != nil {
		return err
	}
	rawRules, err := append(diff.BuiltinRules(), own.Rules()...).JSON()
	if err != nil {
		return err
	}
	return q.BeginPlanning(ctx, dbgen.BeginPlanningParams{ID: c.ID, Settings: rawSettings, NoiseRules: rawRules})
}

// planBatch plans the batch of Pages after c's plan cursor and either chains
// the next batch's job or, at the end of the pool, starts the lanes.
func (w *startWorker) planBatch(ctx context.Context, rc *river.Client[pgx.Tx], tx pgx.Tx, q *dbgen.Queries, c dbgen.Check, settings check.Settings) error {
	size := cmp.Or(w.planBatchSize, DefaultPlanBatchSize)
	batch, err := q.ListPlanBatch(ctx, dbgen.ListPlanBatchParams{
		ProjectID: c.ProjectID, AfterID: c.PlanCursor, BatchSize: int32(size),
	})
	if err != nil {
		return err
	}
	var planned []int64
	for _, pg := range batch {
		if !page.Excluded(settings.ExcludePatterns, pg.Origin, pg.Url) {
			planned = append(planned, pg.ID)
		}
	}
	if len(planned) > 0 {
		if _, err := q.PlanFetches(ctx, dbgen.PlanFetchesParams{CheckID: c.ID, ProjectID: c.ProjectID, PageIds: planned}); err != nil {
			return err
		}
	}
	if len(batch) == size { // there may be more: plan them in the next job
		next := batch[len(batch)-1].ID
		if err := q.SetPlanCursor(ctx, dbgen.SetPlanCursorParams{ID: c.ID, PlanCursor: next}); err != nil {
			return err
		}
		_, err := rc.InsertTx(ctx, tx, check.StartCheckArgs{CheckID: c.ID, AfterPageID: next}, nil)
		return err
	}
	if len(batch) > 0 {
		if err := q.SetPlanCursor(ctx, dbgen.SetPlanCursorParams{ID: c.ID, PlanCursor: batch[len(batch)-1].ID}); err != nil {
			return err
		}
	}
	return startLanes(ctx, rc, tx, q, c, settings)
}

// startLanes ends planning: it starts up to max_parallel lanes, each with a
// Fetch already claimed under its job id, and marks the Check running.
func startLanes(ctx context.Context, rc *river.Client[pgx.Tx], tx pgx.Tx, q *dbgen.Queries, c dbgen.Check, settings check.Settings) error {
	pending, err := q.CountPendingFetches(ctx, c.ID)
	if err != nil {
		return err
	}
	lanes := min(int64(settings.MaxParallel), pending)
	for range lanes {
		laneJob, err := rc.InsertTx(ctx, tx, check.LaneArgs{CheckID: c.ID, FetchTimeoutS: settings.TimeoutS}, nil)
		if err != nil {
			return err
		}
		if _, err := q.ClaimNextFetch(ctx, dbgen.ClaimNextFetchParams{
			JobID: pgtype.Int8{Int64: laneJob.Job.ID, Valid: true}, CheckID: c.ID,
		}); err != nil {
			return err
		}
	}
	if lanes == 0 { // nothing to fetch: finish straight away
		if _, err := rc.InsertTx(ctx, tx, check.FinalizeCheckArgs{CheckID: c.ID}, nil); err != nil {
			return err
		}
	}
	return q.MarkCheckRunning(ctx, dbgen.MarkCheckRunningParams{ID: c.ID, Lanes: int32(lanes)})
}
