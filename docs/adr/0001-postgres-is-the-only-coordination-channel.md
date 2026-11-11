# Postgres is the only coordination channel between processes

The system runs as three separate processes, `web`, `scheduler` and `worker` (separate `cmd/` binaries sharing `internal/` packages). They coordinate only through Postgres: shared tables plus a Postgres-backed job queue. No process calls another over HTTP or RPC, and there is no external broker. Postgres is already required and is the single source of truth. Using it as the queue lets the web process insert a Check and enqueue its job in one transaction, and keeps local setup to one dependency under mise.

## Considered Options

- **HTTP/RPC between processes**: rejected because it couples process lifecycles (a worker being down would fail a web request) and needs service discovery and ports for no gain at this scale.
- **External broker (Redis, NATS)**: rejected because it adds a second stateful service to run locally, and enqueueing can't be transactional with the Check row.

## Consequences

- Live progress in the UI comes from polling Postgres (htmx fragment roughly every 2s), not from push events.
- Exactly one `scheduler` and one `web` instance run. The `worker` may scale out because jobs are claimed with `SKIP LOCKED`.
- Migrations are an explicit step run before the daemons start. No long-running process migrates on boot.
