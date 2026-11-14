// Package testfixtures builds rows for tests by calling the real sqlc
// queries, so fixtures cannot drift from the schema. Every builder takes
// options and fills in unique, valid defaults.
package testfixtures

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/page"
	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/ihorbryk/seo-site-checker/internal/user"
)

var counter atomic.Int64

// Password is every fixture User's password.
const Password = "correct horse battery staple"

// Argon2 is the cheap parameter set tests hash with. Hand it to the server
// (web.Options.Argon2) so logins verify fixture Users' hashes without a rehash.
var Argon2 = user.Params{MemoryKiB: 64, Time: 1, Threads: 1}

// Hasher returns a Hasher with the test parameters.
func Hasher() *user.Hasher { return user.NewHasher(Argon2) }

// UserOption customises a User fixture.
type UserOption func(*dbgen.CreateUserParams)

// WithEmail sets the User's email (stored as given: pass it normalised).
func WithEmail(email string) UserOption {
	return func(p *dbgen.CreateUserParams) { p.Email = email }
}

// User inserts a User whose password is Password. Default email: a unique
// user-N@example.test.
func User(t testing.TB, db dbgen.DBTX, opts ...UserOption) dbgen.User {
	t.Helper()
	hash, err := Hasher().Hash(Password)
	if err != nil {
		t.Fatalf("testfixtures: hash: %v", err)
	}
	params := dbgen.CreateUserParams{
		Email:        fmt.Sprintf("user-%d@example.test", counter.Add(1)),
		PasswordHash: hash,
	}
	for _, o := range opts {
		o(&params)
	}
	u, err := dbgen.New(db).CreateUser(context.Background(), params)
	if err != nil {
		t.Fatalf("testfixtures: user: %v", err)
	}
	return u
}

// Session signs u in with the real session.Create and returns the raw cookie
// token.
func Session(t testing.TB, db dbgen.DBTX, u dbgen.User) string {
	t.Helper()
	raw, err := session.Create(context.Background(), db, u.ID, time.Now())
	if err != nil {
		t.Fatalf("testfixtures: session: %v", err)
	}
	return raw
}

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

// WithOwner sets the Project's owner. Without it the Project gets a fresh User.
func WithOwner(u dbgen.User) ProjectOption {
	return func(p *dbgen.CreateProjectParams) { p.OwnerID = u.ID }
}

// Project inserts a Project (without a seed Page; add Pages explicitly).
// Defaults: a unique domain "site-N.test", a daily schedule and a fresh owner.
func Project(t testing.TB, db dbgen.DBTX, opts ...ProjectOption) dbgen.Project {
	t.Helper()
	params := dbgen.CreateProjectParams{
		Domain:   fmt.Sprintf("site-%d.test", counter.Add(1)),
		Schedule: "daily",
	}
	for _, o := range opts {
		o(&params)
	}
	if params.OwnerID == 0 {
		params.OwnerID = User(t, db).ID
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
		Origin:    page.OriginDiscovered,
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
