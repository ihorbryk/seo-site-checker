# River for the job queue, pgx + sqlc for data access, goose owns all migrations

The job queue is River OSS. `web` and `scheduler` are insert-only River clients, and only `worker` processes jobs. Data access is pgx v5 with sqlc-generated queries. The generated code is committed and CI checks it's up to date, and the few dynamic queries are hand-written with pgx. Migrations are goose SQL files, and River's own schema is copied into goose files with `river migrate-get`, so the pre-start step from ADR 0001 is one `goose up` over one history. River gives transactional enqueue on the same pgx pool, so a Check row and its job commit together. sqlc keeps SQL as the source of truth, which suits `bytea` bodies, window queries and `SKIP LOCKED`.

## Considered Options

- **Hand-rolled `SKIP LOCKED` queue**: rejected for the MVP because we'd have to build retries, backoff, unique jobs and stuck-job recovery ourselves. It stays the fallback if River's 0.x API becomes a burden.
- **gue**: smaller feature set and community, with no clear advantage over River.
- **asynq**: Redis-backed, which ADR 0001 rules out.
- **An ORM (GORM, ent, bun)**: hides SQL, which these queries rely on, and fits poorly with River's transactional insert.
- **River's own migrator alongside goose**: two migration steps that could drift apart.

## Consequences

- Per-Project and per-host concurrency limits are a River Pro feature, so they're enforced in the worker or the schema, never by River OSS.
- Upgrading River means generating its new migration SQL into a new goose file.
- Contributors need the sqlc, goose and river CLIs, pinned as mise tools.
