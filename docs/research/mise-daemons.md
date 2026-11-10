# Running Postgres and app processes as daemons with mise

Research for issue #3 (child of #2). Researched 2026-11-10 against the primary sources listed at the bottom. Not verified by running it locally; snippets come from the docs unless marked as a sketch.

## Answer

Yes. Current mise has a **native daemons feature** (`[daemons]` in `mise.toml`, `mise daemons ...` CLI). It is **experimental** and is a front end for **pitchfork**, the supervisor that actually runs the processes. mise installs the tools and provides the environment; pitchfork handles supervision and readiness checks.

mise also ships a **`postgres` service preset**. It installs PostgreSQL through mise (no Docker), initialises a cluster, keeps the data between runs, waits for readiness, and exports `PGHOST`, `PGPORT`, `PGUSER`, `PGDATABASE` and `DATABASE_URL`.

## 1. Native mise daemons (recommended)

Enable (experimental; needs pitchfork >= 2.25.0; `mise daemons start|restart|register` install pitchfork if missing; safe mode must be off; commands must run inside a project):

```toml
[settings]
experimental = true   # or MISE_EXPERIMENTAL=1
```

Declare daemons. `postgres = "18"` is the preset shorthand. Use `exec` in `run` so the process receives stop signals directly:

```toml
[daemons]
postgres = "18"

[daemons.api]
run = "exec npm run dev"
ready_port = 3000
```

Commands: `mise daemons start [NAME...]` (waits until ready), `stop`, `restart`, `ls [--json]`, `status`, `logs`, `tui`, `urls`, `register`, `prune`. Plain `mise daemons start` starts the `default` group:

```toml
[daemon_groups]
default = ["postgres", "api"]
full = ["default", "worker", "search"]
```

Tasks can require daemons (they keep running after the task exits), and a daemon can run a mise task:

```toml
[tasks.test]
daemons = ["postgres"]
run = "go test ./..."

[tasks."dev:worker"]
run = "go run ./cmd/worker"

[daemons.worker]
task = "dev:worker"
```

Readiness keys such as `ready_port` and `ready_cmd` are passed to pitchfork; tasks and dependent daemons wait for them.

## 2. PostgreSQL via the preset

**Install backend.** The preset installs the mise `postgres` tool. The mise registry lists backends in this order: `conda:postgresql`, `vfox:jdx/vfox-postgres`, `asdf:mise-plugins/mise-postgres`. mise uses the first available, so by default this should resolve to the conda-forge package (prebuilt, no compile). I did not run an install to confirm which backend wins on macOS. The vfox and asdf plugins compile PostgreSQL from source (`./configure && make`), need a C compiler plus openssl and readline (on macOS: Xcode CLT and Homebrew `openssl`/`readline`), and both repos are archived and read-only. Set `tool` on the daemon only to force a different backend.

**Per-project data directory.** By default state lives under `$MISE_STATE_DIR/daemons/` (outside the repo, in `data/<daemon-name>/`). To keep it in the checkout:

```toml
[settings]
experimental = true

[daemons.postgres]
preset = "postgres"
version = "18"
port = 5432                  # default; "auto" gives per-worktree ports
data_dir = ".data/postgres"  # add /.data/ to .gitignore
options.database = "seo_checker"
```

**initdb.** The docs say the preset initialises the cluster on first start (there is an initialization marker) but do not name `initdb` or its flags. Preset data initialisation runs before any `init` commands you define. `options.database` (letters, digits, underscores) is created at first init only; changing it later does not create a database in an existing cluster.

**Start and stop:**

```sh
mise daemons start postgres
mise daemons stop postgres
mise daemons logs postgres
```

**Reset:** stop the daemon, delete the whole data directory (not just its contents, because mise refuses to start a preset whose directory exists without the init marker), start again. `mise daemons ls --json` shows the data dir.

**Preset caveats:**

- Local trust auth for the `postgres` user, loopback only, local dev only.
- Refuses to run as root.
- Changing `data_dir` does not move existing data. Removing a git worktree deletes data kept inside it.
- `postgres = "18"` in `[daemons]` is a version request; an explicit `[tools]` entry such as `"18.1"` satisfies it. Instances sharing a tool must use the same version request.

## 3. Whole-stack sketch for this project

Untested; names and commands are placeholders.

```toml
[settings]
experimental = true

[daemons.postgres]
preset = "postgres"
version = "18"
data_dir = ".data/postgres"
options.database = "seo_checker"

[daemons.web]
run = "exec go run ./cmd/web"
ready_port = 8080
depends = ["postgres"]

[daemons.scheduler]
run = "exec go run ./cmd/scheduler"
depends = ["postgres"]

[daemons.worker]
run = "exec go run ./cmd/worker"
depends = ["postgres"]

[daemon_groups]
default = ["postgres", "web", "scheduler", "worker"]
```

`depends` is a pitchfork key and the dev-stack page uses it between `api` and `db`; I did not confirm the exact pass-through from the `[daemons.*]` table in `mise.toml`. Then `mise daemons start` / `mise daemons stop`.

## 4. Pitchfork directly

`mise use pitchfork` (or `cargo install pitchfork-cli --locked`), configured in `pitchfork.toml` via `[daemons.<name>]` tables. Keys include `run`, `dir`, `env`, `mise = true` (wraps in `mise x --`), `depends`, `retry`, `auto = ["start","stop"]` (shell hook follows project entry and exit), `ready_*`, `health_*`, `watch`, `cron`, `hooks`, `stop_signal`, `boot_start`. Commands: `pitchfork start|stop|restart|status|logs|list|tui`. Platforms: macOS, Linux, Windows. It does not install or initialise Postgres, so you would need mise-installed postgres plus your own initdb step (e.g. a mise task). The native route already covers this, so direct use is only worth it for pitchfork features not exposed through mise.

## 5. Start at login on macOS (optional)

Not needed for a dev workflow, since daemons start on demand. If wanted, in the global mise config a `[bootstrap.services.pitchfork]` entry (running `pitchfork supervisor run --boot`, with `requires_tools = true`) installs a user LaunchAgent at `~/Library/LaunchAgents/dev.mise.pitchfork.plist`. Use the absolute mise path (e.g. `/opt/homebrew/bin/mise`). It starts at login, not at boot, and only daemons with `boot_start = true` start then. Without bootstrap: `pitchfork boot enable`. Pick a single owner of login startup.

## 6. Docker fallback

1. mise container daemons (experimental), no Compose file needed. Documented example (Redis):

```toml
[daemons.redis]
image = "redis:7"
port = { auto = true, base = 6379 }
data_mount = "/data"
data_dir = ".data/redis"
ready_cmd = "{{ runtime }} exec {{ container }} redis-cli ping"
```

   A Postgres equivalent would use `image = "postgres:18"`, `data_mount = "/var/lib/postgresql/data"` and `pg_isready`; the pages I read do not show how to pass container env such as `POSTGRES_PASSWORD`, so verify. Requires Docker or Podman already running (Docker Desktop or `podman machine`); mise does not start the runtime or pull images. Not supported on Windows.
2. Plain `docker compose` wrapped in mise tasks (`mise run db:up`). Independent of mise's experimental status.

## Caveats and risks

- The whole daemons feature is marked experimental; syntax and behaviour may change between releases. Pin a mise version for the team.
- Preset docs do not describe initdb flags, locale or encoding. If those matter, inspect the generated config under `$MISE_STATE_DIR/daemons/` after `mise daemons register`.
- Preset auth is trust-only; keep the port on loopback.
- On macOS the vfox/asdf source-build fallbacks need Xcode CLT plus Homebrew `openssl` and `readline`; the conda backend should avoid this.

## Sources

- mise daemons overview: https://mise.jdx.dev/daemons.html (source: https://github.com/jdx/mise/blob/main/docs/daemons.md)
- Service presets (PostgreSQL): https://mise.jdx.dev/daemons/presets.html
- Data and cleanup: https://mise.jdx.dev/daemons/data.html
- Container daemons: https://mise.jdx.dev/daemons/containers.html
- Development stack, start at login: https://mise.jdx.dev/daemons/development-stack.html
- CLI reference: https://mise.jdx.dev/cli/daemons.html
- mise registry entry for postgres: https://github.com/jdx/mise/blob/main/registry/postgres.toml
- pitchfork: https://pitchfork.jdx.dev/ , https://pitchfork.jdx.dev/reference/configuration.html , https://pitchfork.jdx.dev/guides/mise-integration.html , https://pitchfork.jdx.dev/installation.html , https://github.com/jdx/pitchfork
- vfox plugin: https://github.com/jdx/vfox-postgres ; asdf plugin: https://github.com/mise-plugins/mise-postgres
