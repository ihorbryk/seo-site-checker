-- name: GetSnapshotView :one
SELECT s.id, s.page_id, s.check_id, c.number AS check_number, s.fetched_at, s.status, s.headers, s.seo_fields,
    p.url, p.project_id, pr.domain,
    f.duration_ms,
    s.body_sha256,
    b.size AS body_size
FROM snapshots s
JOIN pages p ON p.id = s.page_id
JOIN projects pr ON pr.id = p.project_id
JOIN checks c ON c.id = s.check_id
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
INSERT INTO diffs (snapshot_id, check_id, baseline_id, status_changed, headers_changed, seo_changed, body_changed, seo_changes, tier)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: CheckSnapshotRedirect :one
-- A Page's Snapshot in one Check, reduced to what redirect chains need.
SELECT s.status, COALESCE(s.seo_fields->'redirect_target'->>0, '')::text AS redirect_target
FROM snapshots s JOIN pages p ON p.id = s.page_id
WHERE s.check_id = $1 AND p.project_id = $2 AND p.url = $3;

-- name: ListChangedSnapshotIDsDense :many
-- One page of the Snapshot ids of a Check's Diffs of one tier, by URL: the
-- Project's Pages in URL order (the unique index), each probed for a Diff of the
-- Check. For a tier that is dense among the Project's Pages; the lateral
-- subquery's OFFSET 0 keeps the planner from reordering the join. A Snapshot
-- has its Fetch's id (see 00043).
SELECT h.id FROM pages p
JOIN LATERAL (
    SELECT f.id FROM fetches f JOIN diffs d ON d.snapshot_id = f.id
    WHERE f.check_id = sqlc.arg(check_id) AND f.page_id = p.id AND d.tier = sqlc.arg(tier)::text
    OFFSET 0
) h ON true
WHERE p.project_id = sqlc.arg(project_id)
ORDER BY p.url
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListChangedSnapshotIDsSparse :many
-- The same page for a tier that is sparse: its rows come from diffs_check_tier_idx
-- (which holds no empty Diffs) and only those are sorted. The lateral lookup
-- (OFFSET 0) keeps the planner from joining the Pages in bulk on a stale
-- estimate of how many rows the index holds.
WITH hit AS MATERIALIZED (
    SELECT d.snapshot_id FROM diffs d
    WHERE d.check_id = sqlc.arg(check_id) AND d.tier = sqlc.arg(tier)::text AND d.tier <> 'empty'
)
SELECT hit.snapshot_id FROM hit
CROSS JOIN LATERAL (
    SELECT p.url FROM snapshots s JOIN pages p ON p.id = s.page_id WHERE s.id = hit.snapshot_id OFFSET 0
) u
ORDER BY u.url
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListCheckDiffRows :many
-- The Changed Pages columns of the given Diffs (one page of ListChangedSnapshotIDs), by tier and URL.
SELECT p.url, d.snapshot_id, d.status_changed, d.headers_changed, d.seo_changed,
    d.body_changed, d.seo_changes, d.tier,
    COALESCE(s.seo_fields->'redirect_target'->>0, '')::text AS redirect_target
FROM diffs d
JOIN snapshots s ON s.id = d.snapshot_id
JOIN pages p ON p.id = s.page_id
WHERE d.snapshot_id = ANY(sqlc.arg(snapshot_ids)::bigint[])
ORDER BY CASE d.tier WHEN 'critical' THEN 0 WHEN 'seo' THEN 1 WHEN 'headers' THEN 2 WHEN 'body' THEN 3 ELSE 4 END, p.url;

-- name: GetDiffBaseline :one
-- The Snapshot a Snapshot was compared with; no row for a Page's first Snapshot.
SELECT baseline_id FROM diffs WHERE snapshot_id = $1;
