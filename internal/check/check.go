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
	TriggerManual    = "manual"
	TriggerScheduled = "scheduled"

	StateQueued     = "queued"
	StateRunning    = "running"
	StateCancelling = "cancelling"
	StateFinished   = "finished"
	StateCancelled  = "cancelled"
)

// Active reports whether a Check in state is queued, running or cancelling.
func Active(state string) bool {
	return state == StateQueued || state == StateRunning || state == StateCancelling
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

// StartCheckArgs turns a queued Check into a running one.
type StartCheckArgs struct {
	CheckID int64 `json:"check_id"`
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

// FinalizeCheckArgs finishes a Check once its last lane ended. It is unique
// per Check.
type FinalizeCheckArgs struct {
	CheckID int64 `json:"check_id"`
}

func (FinalizeCheckArgs) Kind() string { return "finalize_check" }

func (FinalizeCheckArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

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
