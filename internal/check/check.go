// Package check creates Checks and defines the River jobs that run them
// (ADR 0003). It is shared by web (and later the scheduler), which only insert
// jobs, and by the worker, which works them.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Triggers and states as stored.
const (
	TriggerManual    = "manual" // pageliterals:ok Check trigger, not a Page origin
	TriggerScheduled = "scheduled"

	StateQueued     = "queued"
	StatePlanning   = "planning"
	StateRunning    = "running"
	StateCancelling = "cancelling"
	StateFinished   = "finished"
	StateCancelled  = "cancelled"
)

// Active reports whether a Check in state is queued, planning, running or
// cancelling.
func Active(state string) bool {
	return state == StateQueued || state == StatePlanning || state == StateRunning || state == StateCancelling
}

// RobotsUnreachable reports whether the robots.txt a Check stored (status 0
// when none, failureKind empty unless the fetch got no response) is
// unreachable in the RFC 9309 sense: a transport error or a 5xx. A Check that
// should have a robots.txt and has none counts as unreachable. Anything else,
// 4xx included, is readable (a 4xx allows everything).
func RobotsUnreachable(status int, failureKind string) bool {
	return failureKind != "" || status == 0 || status >= 500
}

// Header is one custom request header.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Settings is the copy of a Project's Request settings frozen onto a Check
// when it starts.
type Settings struct {
	UserAgent       string   `json:"user_agent"`
	CustomHeaders   []Header `json:"custom_headers"`
	TimeoutS        int      `json:"timeout_s"`
	MaxParallel     int      `json:"max_parallel"`
	RespectRobots   bool     `json:"respect_robots"`
	ExcludePatterns []string `json:"exclude_patterns"`
}

// SettingsOf copies a Project's Request settings.
func SettingsOf(p dbgen.Project) (Settings, error) {
	s := Settings{
		UserAgent:       p.UserAgent,
		TimeoutS:        int(p.TimeoutS),
		MaxParallel:     int(p.MaxParallel),
		RespectRobots:   p.RespectRobots,
		ExcludePatterns: p.ExcludePatterns,
	}
	if err := json.Unmarshal(p.CustomHeaders, &s.CustomHeaders); err != nil {
		return Settings{}, fmt.Errorf("custom headers: %w", err)
	}
	return s, nil
}

// ParseSettings decodes a Check's frozen settings.
func ParseSettings(raw []byte) (Settings, error) {
	var s Settings
	err := json.Unmarshal(raw, &s)
	return s, err
}

// StartCheckArgs turns a queued Check into a running one, one batch of Pages
// at a time (ADR 0006). The first job finds the Check queued and AfterPageID
// 0; each later one plans the Pages after AfterPageID, which must still be the
// Check's plan_cursor, so a duplicate or stale job does nothing.
type StartCheckArgs struct {
	CheckID     int64 `json:"check_id"`
	AfterPageID int64 `json:"after_page_id,omitempty"`
}

func (StartCheckArgs) Kind() string { return "start_check" }

func (StartCheckArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

// LaneArgs is one lane of a Check. FetchTimeoutS is the Check's fetch timeout,
// carried here so the lane's job timeout (that plus 30 s) is known without a query.
type LaneArgs struct {
	CheckID       int64 `json:"check_id"`
	FetchTimeoutS int   `json:"fetch_timeout_s"`
}

func (LaneArgs) Kind() string { return "lane" }

func (LaneArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 3} }

// FinalizeCheckArgs finishes a Check once its last lane ended, one batch of
// Pages at a time (ADR 0007). The first job (AfterPageID 0) is unique per
// Check; each later one finalizes the Pages after AfterPageID, which must
// still be the Check's finalize cursor, so a duplicate or stale job does
// nothing.
type FinalizeCheckArgs struct {
	CheckID     int64 `json:"check_id"`
	AfterPageID int64 `json:"after_page_id,omitempty"`
}

func (FinalizeCheckArgs) Kind() string { return "finalize_check" }

func (FinalizeCheckArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// ExcludeRecomputeArgs recomputes which of a Project's Pages its exclude
// patterns match, one batch at a time (ADR 0009). Saving different patterns
// starts generation Generation at AfterPageID 0; each job handles the Pages
// after AfterPageID, which must still be the Project's exclude_cursor, and
// only while Generation is still the Project's exclude_gen. A duplicate, a
// retry or a job of an older generation therefore does nothing.
type ExcludeRecomputeArgs struct {
	ProjectID   int64 `json:"project_id"`
	Generation  int64 `json:"generation"`
	AfterPageID int64 `json:"after_page_id,omitempty"`
}

func (ExcludeRecomputeArgs) Kind() string { return "exclude_recompute" }

func (ExcludeRecomputeArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

// NewInsertClient returns an insert-only River client, as web and the
// scheduler use. It never works jobs.
func NewInsertClient(pool *pgxpool.Pool) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{})
}

// ErrActive means the Project already has an active Check.
var ErrActive = errors.New("the Project already has an active Check")

// StartTx inserts a queued Check and its StartCheck job through tx. If the
// Project has an active Check the unique index refuses the insert, which
// aborts tx; use Start to have that handled.
func StartTx(ctx context.Context, tx pgx.Tx, rc *river.Client[pgx.Tx], projectID int64, trigger string) (dbgen.Check, error) {
	c, err := dbgen.New(tx).CreateCheck(ctx, dbgen.CreateCheckParams{ProjectID: projectID, Trigger: trigger})
	if err != nil {
		return dbgen.Check{}, err
	}
	if _, err := rc.InsertTx(ctx, tx, StartCheckArgs{CheckID: c.ID}, nil); err != nil {
		return dbgen.Check{}, err
	}
	return c, nil
}

// Start creates a Check and its StartCheck job in one transaction. When the
// Project already has an active Check it returns that Check with ErrActive.
func Start(ctx context.Context, pool *pgxpool.Pool, rc *river.Client[pgx.Tx], projectID int64, trigger string) (dbgen.Check, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return dbgen.Check{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	c, err := StartTx(ctx, tx, rc, projectID, trigger)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "checks_one_active_idx" {
			active, aerr := dbgen.New(pool).GetActiveCheck(ctx, projectID)
			if aerr != nil {
				return dbgen.Check{}, aerr
			}
			return active, ErrActive
		}
		return dbgen.Check{}, err
	}
	return c, tx.Commit(ctx)
}
