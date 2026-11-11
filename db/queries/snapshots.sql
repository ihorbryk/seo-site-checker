-- name: GetSnapshotView :one
SELECT s.id, s.page_id, s.check_id, s.fetched_at, s.status, s.headers, s.seo_fields,
    p.url, p.project_id, pr.domain,
    f.duration_ms,
    s.body_sha256,
    b.size AS body_size
FROM snapshots s
JOIN pages p ON p.id = s.page_id
JOIN projects pr ON pr.id = p.project_id
JOIN fetches f ON f.id = s.id
LEFT JOIN bodies b ON b.project_id = p.project_id AND b.sha256 = s.body_sha256
WHERE s.id = $1;

-- name: GetBodyContent :one
SELECT content FROM bodies WHERE project_id = $1 AND sha256 = $2;

-- name: PreviousSnapshotID :one
-- The Page's Snapshot just before this one.
SELECT s.id FROM snapshots s
WHERE s.page_id = sqlc.arg(page_id)
  AND (s.fetched_at, s.id) < (sqlc.arg(fetched_at)::timestamptz, sqlc.arg(id)::bigint)
ORDER BY s.fetched_at DESC, s.id DESC LIMIT 1;

-- name: NextSnapshotID :one
-- The Page's Snapshot just after this one.
SELECT s.id FROM snapshots s
WHERE s.page_id = sqlc.arg(page_id)
  AND (s.fetched_at, s.id) > (sqlc.arg(fetched_at)::timestamptz, sqlc.arg(id)::bigint)
ORDER BY s.fetched_at, s.id LIMIT 1;

-- name: LatestSnapshotOfPage :one
-- The Page's newest Snapshot: the baseline for the one about to be stored.
SELECT id, headers, seo_fields, body_sha256 FROM snapshots
WHERE page_id = $1
ORDER BY fetched_at DESC, id DESC LIMIT 1;

-- name: InsertDiff :exec
INSERT INTO diffs (snapshot_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, seo_changes, tier)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListChangedPages :many
-- A Check's non-empty Diffs, most significant first, then by URL.
SELECT p.id AS page_id, p.url, d.snapshot_id, d.status_changed, d.headers_changed, d.seo_changed,
    d.body_changed, d.seo_changes, d.tier
FROM diffs d
JOIN snapshots s ON s.id = d.snapshot_id
JOIN pages p ON p.id = s.page_id
WHERE s.check_id = $1 AND d.tier <> 'empty'
ORDER BY CASE d.tier WHEN 'critical' THEN 0 WHEN 'seo' THEN 1 WHEN 'headers' THEN 2 ELSE 3 END, p.url;

-- name: CountDiffs :one
SELECT
    count(*) FILTER (WHERE d.tier <> 'empty')::bigint AS changed,
    count(*) FILTER (WHERE d.tier = 'empty')::bigint AS unchanged
FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id
WHERE s.check_id = $1;

-- name: GetDiffBaseline :one
-- The Snapshot a Snapshot was compared with; no row for a Page's first Snapshot.
SELECT baseline_id FROM diffs WHERE snapshot_id = $1;
