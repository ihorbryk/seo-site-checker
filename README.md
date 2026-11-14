# SEO Site Checker

Watches your sites and tells you what changed for SEO between one check and the next.

Each monitored site is a **Project**. On its schedule (or on demand) the checker runs a **Check**: one pass over the Project's **Page pool**, which starts at the domain's root and grows through **URL discovery** of same-domain links. Every response is kept as a **Snapshot**. Each Page's newest Snapshot is compared with its previous one to produce a **Diff**: SEO fields (title, meta description, canonical, robots, h1, hreflang, structured-data types, status, redirects and more) plus the raw HTML and headers, with known-volatile parts masked by **Noise rules**. A Check ends in a **Report** that ranks changed Pages and lists new, vanished, retired and failed Pages.

The exact meaning of every term is in [`GLOSSARY.md`](GLOSSARY.md).

> **Status:** early, single-user and meant to run locally. There is no authentication and no deployment setup, so don't expose the web UI publicly.

## Architecture

Three Go processes, `web`, `scheduler` and `worker` (in `cmd/`), which coordinate only through Postgres: shared tables plus a [River](https://riverqueue.com) job queue. Data access uses pgx + sqlc, and migrations are goose files. The reasoning is in [`docs/adr/`](docs/adr/).

## Quickstart

Requires [mise](https://mise.jdx.dev). It installs Go, Postgres, goose, sqlc and the River CLI at the pinned versions.

```sh
mise install
mise run dev      # start postgres, migrate, seed, then web, scheduler and worker
```

Open <http://127.0.0.1:8089>. Stop everything with `mise run stop`.

To get a demo User and Project on first run, set `DEMO_DOMAIN`, `DEMO_EMAIL` and `DEMO_PASSWORD` (at least 15 characters) in `mise.local.toml` before `mise run dev`, then sign in with them:

```toml
[env]
DEMO_DOMAIN = "example.com"
DEMO_EMAIL = "demo@example.com"
DEMO_PASSWORD = "a password of 15+ chars"
```

## Users

An Operator creates Users and resets passwords from the shell. The password is
read from a no-echo prompt (with confirmation) on a terminal, otherwise from one
line of stdin; never from a flag or environment variable.

```sh
mise run user:create me@example.com
mise run user:reset-password me@example.com    # also signs the User out everywhere
```

## Configuration

Environment variables only. `mise.toml` supplies the development defaults, and per-developer overrides go in `mise.local.toml`.

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required (mise points it at the local daemon) | Postgres connection string |
| `WEB_ADDR` | `127.0.0.1:8089` | Address the web UI listens on |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `WORKER_CONCURRENCY` | `20` | Jobs one worker process runs at once |
| `ARGON2_MEMORY_KIB` | `19456` | argon2id memory cost in KiB for password hashing |
| `ARGON2_TIME` | `2` | argon2id iterations |
| `ARGON2_THREADS` | `1` | argon2id parallelism |
| `INSECURE_COOKIES` | `false` | `true` serves a plain `session` cookie without `Secure` and sends no HSTS, for local development over `http://`. `mise.toml` sets it for dev; leave it unset in production |
| `DENY_REGISTRATION` | `false` | `true` closes self-registration: `/register` returns 404 and `/login` says accounts are created by the operator |
| `TRUSTED_PROXY_HEADER` | unset | Header holding the client IP behind a reverse proxy (e.g. `X-Forwarded-For`); its rightmost entry keys the sign-in and registration rate limits. Unset uses the connection's address. Set it only when the proxy overwrites the header, or clients can spoof it |
| `BOOTSTRAP_EMAIL` | unset | Read by the migration that gives Projects owners: an existing install's Projects pass to a locked User with this email (reset its password with `mise run user:reset-password`). Required only when Projects exist; a fresh install needs nothing |
| `DEMO_DOMAIN` | unset | Used by `mise run seed` only: creates a manual-only demo Project for this domain |
| `DEMO_EMAIL` | unset | `seed` only: the demo User who owns the demo Project (created if missing) |
| `DEMO_PASSWORD` | unset | `seed` only: the demo User's password when it is created; an existing User's password is never changed. `seed` does nothing unless all three `DEMO_*` variables are set |

## Development

- Tests, the shared Postgres, migrations, sqlc and lint: [`docs/testing.md`](docs/testing.md)
- Review conventions: [`CODING_STANDARDS.md`](CODING_STANDARDS.md)
- Domain language: [`GLOSSARY.md`](GLOSSARY.md)
- Architecture decisions: [`docs/adr/`](docs/adr/)
- Instructions for coding agents: [`AGENTS.md`](AGENTS.md)

`mise tasks` lists every task.

## License

[MIT](LICENSE)
