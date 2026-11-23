-- name: CountPagePool :one
-- The six Page pool filter counts in one aggregate over the Project's Pages.
-- Each condition repeats the precedence of web.poolState; a test pins them.
SELECT count(*)::bigint AS total,
    (count(*) FILTER (WHERE state = 'active' AND NOT excluded))::bigint AS active,
    (count(*) FILTER (WHERE state = 'active' AND NOT excluded AND orphan_streak > 0))::bigint AS orphaned,
    (count(*) FILTER (WHERE state = 'retired'))::bigint AS retired,
    (count(*) FILTER (WHERE state = 'ignored'))::bigint AS ignored,
    (count(*) FILTER (WHERE state = 'active' AND excluded))::bigint AS excluded
FROM pages WHERE project_id = $1;

-- name: ListPagePoolAll :many
-- One page of Page ids per Page pool filter, in URL order. Each filter is its
-- own query so its WHERE matches a partial index (pages_pool_*_idx).
SELECT id FROM pages WHERE project_id = sqlc.arg(project_id)
ORDER BY url LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListPagePoolActive :many
SELECT id FROM pages WHERE project_id = sqlc.arg(project_id) AND state = 'active' AND NOT excluded
ORDER BY url LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListPagePoolOrphaned :many
SELECT id FROM pages WHERE project_id = sqlc.arg(project_id) AND state = 'active' AND NOT excluded AND orphan_streak > 0
ORDER BY url LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListPagePoolRetired :many
SELECT id FROM pages WHERE project_id = sqlc.arg(project_id) AND state = 'retired'
ORDER BY url LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListPagePoolIgnored :many
SELECT id FROM pages WHERE project_id = sqlc.arg(project_id) AND state = 'ignored'
ORDER BY url LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListPagePoolExcluded :many
SELECT id FROM pages WHERE project_id = sqlc.arg(project_id) AND state = 'active' AND excluded
ORDER BY url LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListPagePoolRows :many
-- The Page pool tab's columns for the given Pages, by URL. last_status is 0 for a Page with no
-- Snapshot yet; last_changed_snapshot_id is 0 when no Snapshot of the Page has
-- a non-empty Diff; first_check_number is 0 for a Page never fetched yet.
SELECT pg.id, pg.url, pg.state, pg.origin, pg.orphan_streak, pg.excluded,
    COALESCE(last.status, 0)::int AS last_status,
    COALESCE(changed.snapshot_id, 0)::bigint AS last_changed_snapshot_id,
    COALESCE(changed.fetched_at, 'epoch'::timestamptz)::timestamptz AS last_changed_at,
    (SELECT count(*) FROM snapshots s WHERE s.page_id = pg.id)::bigint AS snapshots,
    COALESCE((SELECT c.number FROM fetches f JOIN checks c ON c.id = f.check_id
              WHERE f.page_id = pg.id ORDER BY f.check_id LIMIT 1), 0)::bigint AS first_check_number
FROM pages pg
LEFT JOIN LATERAL (
    SELECT s.status FROM snapshots s WHERE s.page_id = pg.id
    ORDER BY s.fetched_at DESC, s.id DESC LIMIT 1
) last ON true
LEFT JOIN LATERAL (
    SELECT s.id AS snapshot_id, s.fetched_at FROM snapshots s JOIN diffs d ON d.snapshot_id = s.id
    WHERE s.page_id = pg.id AND d.tier <> 'empty'
    ORDER BY s.fetched_at DESC, s.id DESC LIMIT 1
) changed ON true
WHERE pg.id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY pg.url;

-- name: AddManualPage :one
-- Adds the Page by hand. An existing discovered or retired Page becomes a
-- manual, active one (never retired again); an ignored Page is left alone
-- (no row returned): the user restores it explicitly.
INSERT INTO pages (project_id, url, origin, excluded)
VALUES (sqlc.arg(project_id), sqlc.arg(url), 'manual', sqlc.arg(excluded))
ON CONFLICT (project_id, url) DO UPDATE SET
    origin = CASE WHEN pages.origin = 'seed' THEN 'seed' ELSE 'manual' END,
    state = 'active',
    orphan_streak = 0
WHERE pages.state <> 'ignored'
RETURNING *;

-- name: GetPage :one
SELECT pg.*, pr.domain,
    COALESCE((SELECT c.number FROM fetches f JOIN checks c ON c.id = f.check_id
              WHERE f.page_id = pg.id ORDER BY f.check_id LIMIT 1), 0)::bigint AS first_check_number
FROM pages pg JOIN projects pr ON pr.id = pg.project_id
WHERE pg.id = $1;

-- name: IgnorePage :execrows
-- The seed Page is never ignored.
UPDATE pages SET state = 'ignored' WHERE id = $1 AND origin <> 'seed' AND state <> 'ignored';

-- name: RestorePage :execrows
UPDATE pages SET state = 'active', orphan_streak = 0 WHERE id = $1 AND state = 'ignored';

-- name: CountPageHistory :one
SELECT count(*)::bigint FROM fetches WHERE page_id = $1 AND state = 'done';

-- name: ListPageHistory :many
-- One page of the Page's history: one row per Check that has a Fetch of the
-- Page, newest first.
SELECT f.check_id, c.number AS check_number, c.started_at AS check_started_at,
    f.outcome, f.skip_reason, f.failure_kind, f.message, f.duration_ms,
    s.id AS snapshot_id, s.status, b.size AS body_size,
    d.tier, d.status_changed, d.headers_changed, d.seo_changed, d.body_changed,
    COALESCE((SELECT array_agg(e.kind ORDER BY e.kind) FROM page_events e
              WHERE e.check_id = f.check_id AND e.page_id = f.page_id), '{}')::text[] AS events
FROM fetches f
JOIN checks c ON c.id = f.check_id
LEFT JOIN snapshots s ON s.id = f.id
LEFT JOIN diffs d ON d.snapshot_id = s.id
LEFT JOIN bodies b ON b.project_id = (SELECT project_id FROM pages WHERE id = f.page_id) AND b.sha256 = s.body_sha256
WHERE f.page_id = sqlc.arg(page_id) AND f.state = 'done'
ORDER BY f.check_id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListProjectSummaries :many
-- Projects with their active Page count, newest Check (any state) and the
-- counts of the newest ended Check's Report, for one owner's Projects.
SELECT p.*,
    (SELECT count(*) FROM pages g WHERE g.project_id = p.id AND g.state = 'active')::bigint AS active_pages,
    COALESCE(lc.number, 0)::bigint AS last_check_number,
    COALESCE(lc.state, '')::text AS last_check_state,
    COALESCE(rc.number, 0)::bigint AS report_check_number,
    COALESCE(rc.count_diff_critical + rc.count_diff_seo + rc.count_diff_headers + rc.count_diff_body, 0)::bigint AS report_changed,
    COALESCE(rc.count_diff_critical, 0)::bigint AS report_critical,
    COALESCE(rc.count_diff_seo, 0)::bigint AS report_seo,
    COALESCE(rc.count_failures, 0)::bigint AS report_failed,
    COALESCE(rc.seed_failed, false)::boolean AS report_seed_failed
FROM projects p
LEFT JOIN LATERAL (SELECT c.id, c.number, c.state FROM checks c WHERE c.project_id = p.id ORDER BY c.created_at DESC, c.id DESC LIMIT 1) lc ON true
LEFT JOIN LATERAL (SELECT c.id, c.number, c.count_failures, c.count_diff_critical, c.count_diff_seo, c.count_diff_headers, c.count_diff_body, c.seed_failed FROM checks c WHERE c.project_id = p.id AND c.state IN ('finished', 'cancelled') ORDER BY c.created_at DESC, c.id DESC LIMIT 1) rc ON true
WHERE p.owner_id = sqlc.arg(owner_id)
ORDER BY p.domain;

-- name: LockProjectShared :one
-- Read by every insert of a Page, to the end of its transaction, so a save of
-- the exclude patterns (which updates the row) waits for it and no Page keeps
-- an answer from older patterns (ADR 0009).
SELECT exclude_patterns FROM projects WHERE id = $1 FOR SHARE;

-- name: LockProjectForExclusion :one
SELECT * FROM projects WHERE id = $1 FOR NO KEY UPDATE;

-- name: StartExcludeRecompute :one
-- A new generation, from the first Page on. Chains of older generations stop.
UPDATE projects SET exclude_gen = exclude_gen + 1, exclude_cursor = 0
WHERE id = $1 RETURNING exclude_gen;

-- name: ListExcludeBatch :many
SELECT id, url, origin, excluded FROM pages
WHERE project_id = sqlc.arg(project_id) AND id > sqlc.arg(after_id)
ORDER BY id LIMIT sqlc.arg(batch_size);

-- name: SetPagesExcluded :exec
-- Writes only the answers that change.
UPDATE pages SET excluded = f.excluded
FROM unnest(sqlc.arg(page_ids)::bigint[]) WITH ORDINALITY AS i(id, n)
JOIN unnest(sqlc.arg(flags)::boolean[]) WITH ORDINALITY AS f(excluded, n) USING (n)
WHERE pages.id = i.id AND pages.excluded <> f.excluded;

-- name: SetExcludeDone :exec
-- The recompute of generation $2 reached the Project's last Page.
UPDATE projects SET exclude_done_gen = $2 WHERE id = $1;

-- name: ListUnfinishedExclusions :many
-- Projects whose latest recompute has not reached its last Page and that have
-- no job to continue it (ADR 0009): the job was discarded, or was failed by an
-- old worker that does not know the kind.
SELECT p.id FROM projects p
WHERE p.exclude_gen > p.exclude_done_gen
  AND NOT EXISTS (
    SELECT 1 FROM river_job j
    WHERE j.kind = 'exclude_recompute'
      AND j.state IN ('available', 'scheduled', 'running', 'retryable', 'pending')
      AND (j.args->>'project_id')::bigint = p.id)
ORDER BY p.id;

-- name: SetExcludeCursor :exec
UPDATE projects SET exclude_cursor = $2 WHERE id = $1;
