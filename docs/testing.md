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

# The system harness (`internal/systemtest`)

Scenario tests (Checks, discovery, Reports, ...) go through the in-process
system harness, not through internals. `systemtest.New(t)` gives a fresh
per-test database with:

- the web router (`s.Web`, driven through `httptest`),
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

Conventions:

- Assert what a user sees (rendered pages through `s.Get`/`s.Page`), falling
  back to rows only for what no screen shows yet (stored bodies, snapshots).
- Lanes run concurrently: assert sets and counts, never order.
- Faults come from `fakesite` (`Fault{Delay, Unreachable, CloseMidResponse}`,
  `OversizedBody`); change a site between Checks with `Set`/`Remove`/`Fault`.
- State with no UI yet (settings, until the Settings tab exists) is set with
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
