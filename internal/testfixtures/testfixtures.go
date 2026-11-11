// Package testfixtures builds rows for tests by calling the real sqlc
// queries, so fixtures cannot drift from the schema. Every builder takes
// options and fills in unique, valid defaults.
package testfixtures

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
)

var counter atomic.Int64

// ProjectOption customises a Project fixture.
type ProjectOption func(*dbgen.CreateProjectParams)

// WithDomain sets the Project's domain.
func WithDomain(domain string) ProjectOption {
	return func(p *dbgen.CreateProjectParams) { p.Domain = domain }
}

// WithSchedule sets the Project's schedule (manual, hourly, daily, weekly).
func WithSchedule(schedule string) ProjectOption {
	return func(p *dbgen.CreateProjectParams) { p.Schedule = schedule }
}

// Project inserts a Project (without a seed Page; add Pages explicitly).
// Defaults: a unique domain "site-N.test" and a daily schedule.
func Project(t testing.TB, db dbgen.DBTX, opts ...ProjectOption) dbgen.Project {
	t.Helper()
	params := dbgen.CreateProjectParams{
		Domain:   fmt.Sprintf("site-%d.test", counter.Add(1)),
		Schedule: "daily",
	}
	for _, o := range opts {
		o(&params)
	}
	p, err := dbgen.New(db).CreateProject(context.Background(), params)
	if err != nil {
		t.Fatalf("testfixtures: project: %v", err)
	}
	return p
}

// PageOption customises a Page fixture.
type PageOption func(*dbgen.CreatePageParams)

// WithOrigin sets the Page's origin (seed, discovered, manual).
func WithOrigin(origin string) PageOption {
	return func(p *dbgen.CreatePageParams) { p.Origin = origin }
}

// Page inserts a Page of the Project at path on the Project's domain
// (e.g. "/about"). Default origin: discovered.
func Page(t testing.TB, db dbgen.DBTX, project dbgen.Project, path string, opts ...PageOption) dbgen.Page {
	t.Helper()
	params := dbgen.CreatePageParams{
		ProjectID: project.ID,
		Url:       "https://" + project.Domain + path,
		Origin:    "discovered",
	}
	for _, o := range opts {
		o(&params)
	}
	p, err := dbgen.New(db).CreatePage(context.Background(), params)
	if err != nil {
		t.Fatalf("testfixtures: page: %v", err)
	}
	return p
}
