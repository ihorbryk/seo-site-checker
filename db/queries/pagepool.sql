-- name: ListPagePoolRows :many
-- The Page pool with what its tab shows. last_status is 0 for a Page with no
-- Snapshot yet; last_changed_snapshot_id is 0 when no Snapshot of the Page has
-- a non-empty Diff; first_check_id is 0 for a Page never fetched yet.
SELECT pg.id, pg.url, pg.state, pg.origin, pg.orphan_streak,
    COALESCE(last.status, 0)::int AS last_status,
    COALESCE(changed.snapshot_id, 0)::bigint AS last_changed_snapshot_id,
    COALESCE(changed.fetched_at, 'epoch'::timestamptz)::timestamptz AS last_changed_at,
    (SELECT count(*) FROM snapshots s WHERE s.page_id = pg.id)::bigint AS snapshots,
    COALESCE((SELECT min(f.check_id) FROM fetches f WHERE f.page_id = pg.id), 0)::bigint AS first_check_id
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
WHERE pg.project_id = $1 ORDER BY pg.url;

-- name: AddManualPage :one
-- Adds the Page by hand. An existing discovered or retired Page becomes a
-- manual, active one (never retired again); an ignored Page is left alone
-- (no row returned): the user restores it explicitly.
INSERT INTO pages (project_id, url, origin)
VALUES (sqlc.arg(project_id), sqlc.arg(url), 'manual')
ON CONFLICT (project_id, url) DO UPDATE SET
    origin = CASE WHEN pages.origin = 'seed' THEN 'seed' ELSE 'manual' END,
    state = 'active',
    orphan_streak = 0
WHERE pages.state <> 'ignored'
RETURNING *;

-- name: GetPage :one
SELECT pg.*, pr.domain, pr.exclude_patterns,
    COALESCE((SELECT min(f.check_id) FROM fetches f WHERE f.page_id = pg.id), 0)::bigint AS first_check_id
FROM pages pg JOIN projects pr ON pr.id = pg.project_id
WHERE pg.id = $1;

-- name: IgnorePage :execrows
-- The seed Page is never ignored.
UPDATE pages SET state = 'ignored' WHERE id = $1 AND origin <> 'seed' AND state <> 'ignored';

-- name: RestorePage :execrows
UPDATE pages SET state = 'active', orphan_streak = 0 WHERE id = $1 AND state = 'ignored';

-- name: ListPageHistory :many
-- One row per Check that has a Fetch of the Page, newest first.
SELECT f.check_id, c.started_at AS check_started_at,
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
WHERE f.page_id = $1 AND f.state = 'done'
ORDER BY f.check_id DESC;

-- name: ListProjectSummaries :many
-- Projects with their active Page count, newest Check (any state) and the
-- counts of the newest ended Check's Report.
SELECT p.*,
    (SELECT count(*) FROM pages g WHERE g.project_id = p.id AND g.state = 'active')::bigint AS active_pages,
    COALESCE(lc.id, 0)::bigint AS last_check_id,
    COALESCE(lc.state, '')::text AS last_check_state,
    COALESCE(rc.id, 0)::bigint AS report_check_id,
    COALESCE((SELECT count(*) FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id
              WHERE s.check_id = rc.id AND d.tier <> 'empty'), 0)::bigint AS report_changed,
    COALESCE((SELECT count(*) FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id
              WHERE s.check_id = rc.id AND d.tier = 'critical'), 0)::bigint AS report_critical,
    COALESCE((SELECT count(*) FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id
              WHERE s.check_id = rc.id AND d.tier = 'seo'), 0)::bigint AS report_seo,
    COALESCE(rc.count_failures, 0)::bigint AS report_failed,
    EXISTS (SELECT 1 FROM fetches f JOIN pages g ON g.id = f.page_id
            WHERE f.check_id = rc.id AND g.origin = 'seed' AND f.outcome = 'failure') AS report_seed_failed
FROM projects p
LEFT JOIN LATERAL (SELECT c.id, c.state FROM checks c WHERE c.project_id = p.id ORDER BY c.created_at DESC, c.id DESC LIMIT 1) lc ON true
LEFT JOIN LATERAL (SELECT c.id, c.count_failures FROM checks c WHERE c.project_id = p.id AND c.state IN ('finished', 'cancelled') ORDER BY c.created_at DESC, c.id DESC LIMIT 1) rc ON true
ORDER BY p.domain;
