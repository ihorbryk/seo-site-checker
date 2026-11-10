# Go libraries for the SEO site checker stack

Research for issue #4 (child of map #2). Not a decision: a later grilling ticket locks the stack.

Stack context: Go 1.27, server-rendered htmx UI, PostgreSQL, separate scheduler / fetch-worker / web processes coordinating via Postgres. Domain: fetch pages, store HTML + headers as Snapshots, extract SEO fields and same-host links, show Diffs between consecutive Snapshots.

Maintenance data below was read from the GitHub API (`repos/<r>`, `releases/latest`, `go.mod`) on 2026-11-10. Claims marked **(from memory, verify)** are well-known behaviour I could not re-read from a primary page in this session.

## Summary table

| Slot | Tentative pick | Alternatives |
|---|---|---|
| HTTP router | `net/http` ServeMux | chi v5, echo v5 |
| Templating | `html/template` | templ |
| Postgres access | pgx v5 + sqlc | pgx only, ORM (GORM/ent/bun) |
| Migrations | goose | golang-migrate, atlas |
| Text/HTML diff | `go-udiff` for line diff + own HTML renderer | sergi/go-diff, plain Go set diff on extracted fields |
| HTML parsing | goquery (or `golang.org/x/net/html` directly) | x/net/html |
| URL normalisation | `net/url` + small hand-written normaliser (+ `x/net/idna`) | purell, urlx |
| Job queue | river | gue, hand-rolled `SKIP LOCKED` (asynq ruled out: Redis) |

## 1. HTTP router

Facts (verified, GitHub):
- go-chi/chi: v5.5.0 released 2026-10-14, last push 2026-11-07, MIT, ~23k stars. go.mod states it supports the four most recent major Go versions.
- labstack/echo: v5.4.4 released 2026-10-30, MIT, ~33k stars. README: v5 is the current major (released 2026-01-18); v4 gets bug fixes to 2026-12-31 and security fixes to 2027-12-31; new projects should use v5. v5 supports the three most recent Go releases.
- `net/http` ServeMux: standard library; since Go 1.22 supports method + wildcard patterns (`GET /pages/{id}`, `{$}`, `{path...}`) **(from memory, verify against go.dev/blog/routing-enhancements)**. The pkg.go.dev `net/http` page confirms `Request.PathValue` / `SetPathValue` exist.

| Option | Pros | Cons |
|---|---|---|
| ServeMux | zero dependency, always current with Go 1.27, method/wildcard routing is enough for a small htmx app | no built-in route groups or middleware chaining (write a small `Use` helper) |
| chi v5 | thin layer over `net/http` (handlers stay `http.Handler`), route groups, middleware set (logger, recoverer, timeout, request ID), actively released | extra dependency for things ServeMux mostly does now |
| echo v5 | batteries (binding, validation helpers, middleware) | own `Context` type, not plain `http.Handler`; v4 to v5 migration churn just happened; more framework than an htmx/HTML app needs |

Tentative recommendation: ServeMux, with chi as the fallback if middleware grouping becomes painful. Both keep handlers portable.

## 2. Server-side templating

Facts:
- `html/template`: stdlib, contextual auto-escaping.
- a-h/templ: v0.3.1100 released 2026-10-26, last push 2026-10-30, MIT, ~10.6k stars, go.mod needs Go 1.26. Docs (templ.guide): components compile to Go code, typed parameters, no client JS needed. Still 0.x versioned (verified from tag). Requires a `templ generate` code-generation step **(from memory, verify)**; the docs page I fetched did not describe it or htmx.

| Option | Pros | Cons |
|---|---|---|
| html/template | no build step, no dependency, proven escaping, `{{block}}` / `{{define}}` partials suit htmx fragment responses | runtime errors for typos/wrong fields, weak refactoring/IDE support |
| templ | compile-time type checking, components are plain Go functions (easy to render a fragment for htmx or a full page), good fit for a diff view built from many small components | codegen step in CI/dev loop, 0.x versioning (API breaks possible), extra tooling (`templ` CLI, editor plugin) |

Tentative recommendation: html/template to keep the MVP toolchain minimal; templ is the strong alternative if the diff UI grows complex. Templates are the only layer affected by a later switch.

## 3. Postgres access

Facts:
- jackc/pgx: v5.11.0 released 2026-09-07, last push 2026-11-07, MIT, ~14k stars. Native Postgres driver with pool (`pgxpool`), `COPY`, LISTEN/NOTIFY **(from memory, verify)**.
- sqlc-dev/sqlc: v1.31.1 released 2026-04-22, last push 2026-11-03, MIT, ~18k stars, go.mod Go 1.26. Generates type-safe Go from SQL; has a pgx/v5 output mode **(from memory, verify: the docs page I fetched was only a redirect to `/en/v1.31.1/guides/using-go-and-pgx.html`)**.
- ORMs (GORM, ent, bun): not researched in depth. The data is a few tables (Sites, Pages, Snapshots, extracted fields), append-heavy, with a few diff-oriented queries.

| Option | Pros | Cons |
|---|---|---|
| pgx alone | full Postgres feature access, River's `riverpgxv5` driver shares the same pool | hand-written scanning boilerplate; no compile-time query checking |
| pgx + sqlc | SQL stays the source of truth, queries type-checked against the schema, no runtime reflection | codegen step; dynamic/optional-filter queries awkward; ~6 months since the last sqlc release |
| ORM | fast CRUD | hides SQL; awkward for large bodies, window queries for "consecutive Snapshots", `SKIP LOCKED`; weaker fit with River's transactional insert |

Tentative recommendation: pgx v5 with sqlc. If River is chosen, pgx v5 is the natural driver anyway (River supports pgx v5 or `database/sql`, per riverqueue.com docs).

## 4. Migrations

Facts:
- pressly/goose: v3.30.0 released 2026-11-10, MIT, ~11.7k stars, go.mod Go 1.26. Release notes: SQL parser improvements, distroless container image, Go-migration CLI fix. Supports SQL and Go migrations and in-process use **(from memory, verify)**.
- golang-migrate/migrate: v4.20.1 released 2026-09-09, MIT, ~19k stars, go.mod Go 1.25.
- ariga/atlas: v1.3.0 released 2026-08-02, Apache-2.0 (per README), ~8.8k stars. README: declarative schema (HCL/SQL/ORM) plus versioned migrations; examples use a `--dev-url` such as `docker://postgres/16/dev`. The README did not say which features need Atlas Cloud or a paid plan; check before relying on lint/CI features.

| Option | Pros | Cons |
|---|---|---|
| goose | plain SQL files, embeddable and runnable in-process, actively released, Go migrations for data backfills | no declarative diffing |
| golang-migrate | widely used, simple up/down SQL files, CLI + library | slower releases; up/down-pair convention; fewer in-process niceties |
| atlas | schema diffing, linting for destructive changes | dev-database requirement, heavier workflow, cloud/commercial edges unclear, overkill for a handful of tables |

Tentative recommendation: goose. sqlc can read goose-style migration SQL **(from memory, verify)**. River ships its own migrations (section 8), which can be run via its migrator or emitted as SQL into goose.

## 5. Text / HTML diff and HTML rendering of diffs

Facts (GitHub):
- aymanbagabas/go-udiff: v0.4.1, last push 2026-11-08, BSD-3-Clause, ~235 stars. Myers line diff, unified diffs and edit lists; derived from Go tools' internal diff **(from memory, verify)**.
- sergi/go-diff (diffmatchpatch): tag v1.4.0, last push 2025-06-05, MIT, ~2k stars. Character/word-level diff with a `DiffPrettyHtml` helper **(from memory, verify)**; no GitHub Releases.
- pmezard/go-difflib: last push 2023-05-30, effectively unmaintained (used inside testify). Not recommended.
- hexops/gotextdiff: last push 2023-09, 7 stars. Not recommended.

None of these produces a ready side-by-side or GitHub-style HTML view; they return edit operations. Rendering means looping over ops in the template and emitting `<ins>`/`<del>` or classed spans, which `html/template` escapes automatically (safer than string-building via `DiffPrettyHtml`).

Design note: diff Snapshots at two levels: (a) a structured diff of extracted SEO fields (title, meta description, canonical, headings, link set added/removed) with plain Go set/equality logic, and (b) a line diff of the stored HTML (ideally pretty-printed/normalised first, so minified pages yield meaningful lines).

| Option | Pros | Cons |
|---|---|---|
| go-udiff + own renderer | maintained, small, line-level fits HTML source diffs, full control of markup | you write the renderer (small) |
| sergi/go-diff | intra-line (char/word) highlights, ready HTML helper | slower cadence, last push over a year ago, helper markup won't match site styling |
| plain Go set diff | no dependency | only for fields/links, not the raw HTML body |

Tentative recommendation: go-udiff for line diff of normalised HTML, plain Go set diff for fields/links, a small templated renderer; add go-diff only if word-level highlighting inside changed lines is wanted.

## 6. HTML parsing (links and SEO fields)

Facts:
- golang.org/x/net/html: official Go sub-repo (golang/net, last push 2026-10-27, BSD-3-Clause); HTML5 tokenizer and tree parser, tolerant of broken markup. No selector engine.
- PuerkitoBio/goquery: v1.13.0 released 2026-08-27, last push 2026-11-09, BSD-3-Clause, ~15k stars. jQuery-like selectors on top of x/net/html and andybalholm/cascadia (cascadia last push 2026-11-09).

| Option | Pros | Cons |
|---|---|---|
| x/net/html | no extra dependency, tokenizer mode can stream without building a tree, maximum control | verbose walking code for `<title>`, `<meta name=description>`, `<link rel=canonical>`, `h1..h6`, `a[href]` |
| goquery | terse selectors (`doc.Find("meta[name=description]")`), easy to test | builds full DOM in memory, small extra dependency |

Notes: `<base href>` must be handled manually in either option; `rel=nofollow`, meta robots and the `X-Robots-Tag` header live in different places (HTML vs stored headers).

Tentative recommendation: goquery for development speed; x/net/html is a defensible lean choice. The parser sits behind the extraction module, so the cost of change is low.

## 7. URL normalisation

Facts:
- stdlib `net/url`: `Parse`, `ResolveReference`; no canonicalisation helper. `golang.org/x/net/idna` handles IDN to punycode **(from memory, verify)**.
- PuerkitoBio/purell: v1.2.2 released 2026-05-17, BSD-3-Clause, ~485 stars, go.mod Go 1.25. Normalisation flags (lowercase scheme/host, remove default port, remove fragment, sort query, remove dot segments, ...) **(from memory, verify)**. Revived release after a long quiet period: recent but sparse.
- goware/urlx: v0.3.2 released 2022-06-22, no release in 4 years; aimed at user-typed URLs.

| Option | Pros | Cons |
|---|---|---|
| net/url + own function (~40 lines) | exact rules for "same host" and "same page" under this product's definition, no dependency | you own the edge cases (trailing slash, path case, `index.html`, tracking params) |
| purell | tested flag set | its notion of "safe" may not match product rules (trailing slash, `www.`); sparse history |
| urlx | friendly parsing of scheme-less input | stale releases |

Tentative recommendation: own normaliser over `net/url` and `idna`, with unit tests as the specification; purell as reference or fallback. The normalisation policy (trailing slash, query params, `www.`, http vs https) is a product decision for the grilling session.

## 8. Postgres-backed job queue

Facts:
- riverqueue/river: v0.51.2 released 2026-11-09, last push 2026-11-09, MPL-2.0, ~5.9k stars, go.mod Go 1.26. README/docs: transactional enqueue (job invisible until commit, removed on rollback), retries, scheduled jobs, unique jobs, periodic/cron jobs, multiple queues, batch insert via COPY, River UI, test helpers. Works with pgx v5 or `database/sql`; tested against the three most recent PostgreSQL majors; own migrations run before workers start. Pro-only per riverqueue.com: batching, concurrency limits, dead-letter queues, durable periodic jobs, sequences, workflows. Still 0.x. LISTEN/NOTIFY use and multi-process leader election **(from memory, verify)**: the pages I fetched did not cover them.
- vgarvardt/gue: v6.0.1 released 2026-09-08, last push 2026-11-09, MIT, ~316 stars. Postgres-backed, pgx-based **(from memory, verify)**. Smaller feature set and community.
- hibiken/asynq: v0.26.0 (2026-02-03), MIT, ~13.8k stars, but Redis-backed, which violates "coordinate via Postgres". Ruled out.
- Hand-rolled: `SELECT ... FOR UPDATE SKIP LOCKED` on a jobs table, perhaps 100 lines.

| Option | Pros | Cons |
|---|---|---|
| river | most complete OSS Go option, actively released, transactional enqueue lets the scheduler enqueue a fetch atomically with its bookkeeping, periodic jobs cover the scheduler role, UI available | 0.x API; own tables/migrations; some features Pro-only; MPL-2.0 (file-level copyleft; fine as a dependency) |
| gue | simpler, MIT | far fewer features (no UI, less scheduling), smaller community |
| hand-rolled SKIP LOCKED | full control, no dependency, schema is yours | you build retries/backoff, visibility timeouts, observability |

Domain note: polite crawling needs per-host concurrency/rate limits; River's built-in concurrency limits are Pro, so with OSS River per-host throttling would live in the worker (or per-site queues with capped workers). Worth raising in the grilling session.

Tentative recommendation: river (OSS) for the queue and periodic scheduling; hand-rolled SKIP LOCKED is the minimal alternative if zero new dependencies is preferred.

## Open points for the grilling ticket

1. Is a codegen step (sqlc, templ) acceptable in the contributor workflow?
2. Per-host politeness (rate limit/concurrency): in the queue or in the worker?
3. URL normalisation policy (trailing slash, query params, `www.`).
4. Pretty-print HTML before line-diffing Snapshots?
5. Confirm the "from memory, verify" items before locking.

## Sources

- https://github.com/go-chi/chi, https://github.com/labstack/echo, https://github.com/a-h/templ, https://templ.guide/
- https://github.com/jackc/pgx, https://github.com/sqlc-dev/sqlc, https://docs.sqlc.dev/
- https://github.com/pressly/goose, https://github.com/golang-migrate/migrate, https://github.com/ariga/atlas
- https://github.com/aymanbagabas/go-udiff, https://github.com/sergi/go-diff, https://github.com/pmezard/go-difflib, https://github.com/hexops/gotextdiff
- https://github.com/golang/net, https://github.com/PuerkitoBio/goquery, https://github.com/andybalholm/cascadia
- https://github.com/PuerkitoBio/purell, https://github.com/goware/urlx
- https://github.com/riverqueue/river, https://riverqueue.com/docs, https://github.com/vgarvardt/gue, https://github.com/hibiken/asynq
- https://pkg.go.dev/net/http
