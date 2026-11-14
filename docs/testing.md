# Running the tests

Tests need a Postgres 18 reachable through `DATABASE_URL` and nothing else.
`testdb` builds one template database per migration-set hash (shared by every
process and worktree on that server, race-safe) and gives each test a clone.
The role must be able to `CREATE DATABASE`.

| Situation | Command |
| --- | --- |
| Own daemon (starts the `postgres` daemon if needed) | `mise run test:local` |
| Any Postgres already running, e.g. one shared by several worktrees | `DATABASE_URL=postgres://postgres@127.0.0.1:54329/seo_checker?sslmode=disable mise run test` |
| Same, without mise tasks | `DATABASE_URL=... go test -race ./...` |
| CI | `mise run test` with `DATABASE_URL` pointing at the service container |

`mise.toml` defaults `DATABASE_URL` to the local daemon but keeps a value that
is already in the environment, so `mise run test` never starts a daemon.
Only the `*:local` tasks (`test:local`, `migrate:local`, `seed:local`,
`psql:local`) declare the daemon.

Templates left by older migration sets (`seo_tpl_*`) are harmless; drop them
with `psql "$DATABASE_URL" -Atc "select datname from pg_database where datname like 'seo_tpl_%'"`
and `DROP DATABASE` each.

# Development workflow

- **One shared Postgres.** Parallel worktrees and agents run against one
  server through `DATABASE_URL` (see the table above). Never start or stop
  daemons from a worktree.
- **Migrations**: one per change, made with `mise run migrate:new <name>`
  (renamed to the next sequential number, `000NN_name.sql`, like the others).
  Goose files carry a working `Down`. A migration that reads an env var
  (`BOOTSTRAP_EMAIL`) uses `-- +goose ENVSUB ON`; goose reads the variable
  when the provider parses the files, so a test sets it with `t.Setenv`
  *before* `goose.NewProvider` (see `db/owners_migration_test.go`).
- **sqlc**: queries live in `db/queries/*.sql`. When `internal/dbgen` conflicts
  in a merge or rebase, resolve the `.sql` sources, then run `mise run gen`;
  regenerated output replaces the conflicted files.
- **CSS** (ADR 0004): templates use Tailwind v4 + daisyUI 5.7.50 classes.
  `internal/web/css/input.css` is the source; `mise run css` builds the
  committed `internal/web/static/app.css`, and the `css` daemon rebuilds it on
  template changes. Upgrade daisyUI with `mise run css:daisyui <version>` and
  update the version here. Semantic classes and ids next to the daisyUI ones
  (`.fetched`, `.tier`, `#body-diff`, ...) are systemtest hooks: keep them, and
  keep header rows as plain `<tr>`s since tests count rows by position.
- **Lint**: `mise run lint` runs `lint:fmt`, `lint:vet`, `lint:sqlc`,
  `lint:tidy`, `lint:literals` and `lint:css`; run one by name to see only its
  output. `lint:sqlc`, `lint:tidy` and `lint:css` compare against the working
  tree with `git diff`, so commit regenerated `internal/dbgen`,
  `go.mod`/`go.sum` and `app.css` before `mise run lint`.
- **Guards**: `lint:literals` rejects Page state, origin and event string
  literals outside `internal/page`; mark a same-spelling literal with
  `// pageliterals:ok <reason>`. `internal/worker` has a test that only
  `finalize.go` writes `page_events`.
- Review judgement calls live in `CODING_STANDARDS.md`.

# The system harness (`internal/systemtest`)

Scenario tests (Checks, discovery, Reports, ...) go through the in-process
system harness, not through internals. `systemtest.New(t)` gives a fresh
per-test database with:

- the web router (`s.Web`, driven through `httptest`) with a signed-in default
  User (`s.User`, `s.Browser`, see below),
- a real River worker started against that database,
- a `fakesite.Network` behind the worker's injected `*http.Client`: it dials
  the fake sites you add and fails DNS for every other host.

```go
s := systemtest.New(t)
site := s.Site("shop.test")                          // reachable by the worker
site.Set("/", fakesite.Response{Body: "<html>..</html>"})
p := s.AddProject("shop.test")                       // through the web form
report := s.WaitForCheck(s.RunCheck(p))              // press Run Check, poll until it ends
// assert on the Report with goquery: report.Find("#failures tr") ...
```

Every screen needs a signed-in User, so `systemtest.New` creates one
(`testfixtures.User` + `testfixtures.Session`, cheap argon2 params shared with
`web.Options.Argon2`) and `s.Get`, `s.GetHTMX`, `s.Post` and `s.AddProject`
act as them. They delegate to `s.Browser`, a `*systemtest.Browser`: a User plus
a one-cookie jar. A Browser applies the app's `Set-Cookie`, so signing in or
out through the real `/login` and `/logout` forms changes who it is. Redirects
are not followed.

```go
other := s.NewUser()                 // a second signed-in Browser (other.User)
visitor := s.SignedOut()             // no cookie
w := visitor.PostPlain("/login", url.Values{"email": {s.User.Email}, "password": {testfixtures.Password}})
visitor.Get("/p/1")                  // full page load
visitor.GetHTMX("/p/1")              // hx-get: fragment, no shell
visitor.Post("/projects", form)      // an htmx form post (HX-Request: true)
visitor.PostPlain("/login", form)    // a plain, no-JS form post
visitor.Request("POST", "/projects", form, http.Header{"HX-Current-URL": {"http://x/p/3"}}) // anything else
other.AddProject("shop.test")        // through the form; found by owner + domain
```

`systemtest.New(t, opts...)` takes options that set `web.Options` fields
(`systemtest.InsecureCookies()`); add one per new switch. `WebQueries` leaves
out the session lookup every signed-in request makes. Rows built straight in
the database (`testfixtures.Project` defaults to a fresh owner; pass
`WithOwner(s.User)` to give it to the default User).

Conventions:

- Assert what a user sees (rendered pages through `s.Get`/`s.Page`), falling
  back to rows only for what no screen shows yet (stored bodies, snapshots).
- Lanes run concurrently: assert sets and counts, never order.
- Faults come from `fakesite` (`Fault{Delay, Unreachable, CloseMidResponse}`,
  `OversizedBody`); change a site between Checks with `Set`/`Remove`/`Fault`.
- Request settings are set through the Settings tab form with
  `s.SaveRequestSettings(p, form)`; `site.Requests(path)` returns the headers of
  every request the fake site received.
- State with no UI yet is set with
  `s.Exec("UPDATE projects SET ...")` or `testfixtures`; switch the test to the
  form when the form exists.
- To extend the harness, add a method to `systemtest.System` (e.g. a scheduler
  tick with an explicit `now`, cancel, a Noise rules form) rather than wiring
  pieces together in each test. Needs a new fake host? `s.Site(host)`.
- The worker runs with a 50 ms River poll interval; `WaitForCheck` fails the
  test after 30 s.
- Insert-only sides (web's `check.StartTx`) are tested with
  `rivertest.RequireInsertedTx`, see `internal/check/check_test.go`.
- Leaf logic (URL normalising, error classification, ...) gets plain table
  tests in its own package.
