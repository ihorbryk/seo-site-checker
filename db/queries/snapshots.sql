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
