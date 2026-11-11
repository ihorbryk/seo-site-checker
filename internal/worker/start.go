package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/diff"
	"github.com/ihorbryk/seo-site-checker/internal/exclude"
	projectrules "github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type startWorker struct {
	river.WorkerDefaults[check.StartCheckArgs]
	*deps
}

// Work copies the Request settings onto the Check, marks it running, plans one
// Fetch per active Page and starts up to max_parallel lanes, each with a Fetch
// already claimed under its job id. It is safe to repeat: a Check that is no
// longer queued is left alone.
func (w *startWorker) Work(ctx context.Context, job *river.Job[check.StartCheckArgs]) error {
	// robots.txt is fetched before the planning transaction, so no row lock is
	// held during the request. A retry fetches it again; nothing is stored
	// until the transaction commits.
	pre, err := w.q.GetCheck(ctx, job.Args.CheckID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the Check is gone; nothing to start
		}
		return err
	}
	if pre.State != check.StateQueued {
		return nil
	}
	project, err := w.q.GetProject(ctx, pre.ProjectID)
	if err != nil {
		return err
	}
	settings, err := check.SettingsOf(project)
	if err != nil {
		return err
	}
	var robotsRes *robotsResult
	if settings.RespectRobots {
		res, err := fetchRobots(ctx, w.client, project.Domain, settings)
		if err != nil {
			return err
		}
		robotsRes = &res
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := w.q.WithTx(tx)

	c, err := q.LockCheck(ctx, job.Args.CheckID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the Check is gone; nothing to start
		}
		return err
	}
	if c.State != check.StateQueued {
		return nil
	}
	rawSettings, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if robotsRes != nil {
		if err := storeRobots(ctx, q, c.ID, *robotsRes); err != nil {
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
	pages, err := q.ListPagesByProject(ctx, c.ProjectID)
	if err != nil {
		return err
	}
	var planned []int64
	for _, pg := range pages {
		if pg.State == "active" && (pg.Origin == "seed" || !exclude.Excluded(settings.ExcludePatterns, pg.Url)) {
			planned = append(planned, pg.ID)
		}
	}
	pending, err := q.PlanFetches(ctx, dbgen.PlanFetchesParams{CheckID: c.ID, ProjectID: c.ProjectID, PageIds: planned})
	if err != nil {
		return err
	}
	lanes := min(int64(settings.MaxParallel), pending)

	rc := river.ClientFromContext[pgx.Tx](ctx)
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
	err = q.MarkCheckRunning(ctx, dbgen.MarkCheckRunningParams{
		ID: c.ID, Settings: rawSettings, NoiseRules: rawRules, Lanes: int32(lanes),
	})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
