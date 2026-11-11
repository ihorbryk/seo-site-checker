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

To get a demo Project on first run, set `DEMO_DOMAIN` in `mise.local.toml` before `mise run dev`:

```toml
[env]
DEMO_DOMAIN = "example.com"
```

## Configuration

Environment variables only. `mise.toml` supplies the development defaults, and per-developer overrides go in `mise.local.toml`.

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required (mise points it at the local daemon) | Postgres connection string |
| `WEB_ADDR` | `127.0.0.1:8089` | Address the web UI listens on |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `WORKER_CONCURRENCY` | `20` | Jobs one worker process runs at once |
| `DEMO_DOMAIN` | unset | Used by `mise run seed` only: creates a manual-only demo Project for this domain |

## Development

- Tests, the shared Postgres, migrations, sqlc and lint: [`docs/testing.md`](docs/testing.md)
- Review conventions: [`CODING_STANDARDS.md`](CODING_STANDARDS.md)
- Domain language: [`GLOSSARY.md`](GLOSSARY.md)
- Architecture decisions: [`docs/adr/`](docs/adr/)
- Instructions for coding agents: [`AGENTS.md`](AGENTS.md)

`mise tasks` lists every task.

## License

[MIT](LICENSE)
