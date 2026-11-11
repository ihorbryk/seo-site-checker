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
