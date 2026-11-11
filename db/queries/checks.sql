-- name: CreateCheck :one
INSERT INTO checks (project_id, trigger) VALUES ($1, $2) RETURNING *;

-- name: GetCheck :one
SELECT * FROM checks WHERE id = $1;

-- name: LockCheck :one
SELECT * FROM checks WHERE id = $1 FOR UPDATE;

-- name: GetActiveCheck :one
SELECT * FROM checks
WHERE project_id = $1 AND state IN ('queued', 'running', 'cancelling');

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1;

-- name: MarkCheckRunning :exec
UPDATE checks
SET state = 'running', started_at = now(), settings = $2, noise_rules = $3, lanes = $4
WHERE id = $1;

-- name: PlanFetches :execrows
INSERT INTO fetches (check_id, page_id)
SELECT sqlc.arg(check_id), pg.id FROM pages pg
WHERE pg.project_id = sqlc.arg(project_id) AND pg.state = 'active'
  AND pg.id = ANY(sqlc.arg(page_ids)::bigint[])
ORDER BY pg.id;

-- name: ClaimNextFetch :one
-- Claims one pending Fetch for the lane job, skipping rows other lanes hold.
UPDATE fetches SET state = 'in_flight', job_id = sqlc.arg(job_id), started_at = now()
WHERE id = (
    SELECT f.id FROM fetches f
    WHERE f.check_id = sqlc.arg(check_id) AND f.state = 'pending'
    ORDER BY f.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING id;

-- name: GetClaimedFetch :one
SELECT f.id, f.check_id, f.page_id, p.url, p.project_id
FROM fetches f JOIN pages p ON p.id = f.page_id
WHERE f.job_id = $1 AND f.state = 'in_flight';

-- name: FinishFetchSnapshot :exec
UPDATE fetches SET state = 'done', outcome = 'snapshot', duration_ms = $2 WHERE id = $1;

-- name: FinishFetchFailure :exec
UPDATE fetches
SET state = 'done', outcome = 'failure', failure_kind = $2, message = $3, duration_ms = $4
WHERE id = $1;

-- name: InsertBody :exec
INSERT INTO bodies (project_id, sha256, content, size)
VALUES ($1, $2, $3, $4)
ON CONFLICT (project_id, sha256) DO NOTHING;

-- name: InsertSnapshot :exec
INSERT INTO snapshots (id, page_id, check_id, fetched_at, status, headers, body_sha256, seo_fields)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: SetFetchJob :exec
UPDATE fetches SET job_id = $2 WHERE id = $1;

-- name: SetCheckLanes :exec
UPDATE checks SET lanes = $2 WHERE id = $1;

-- name: FinishCheck :exec
UPDATE checks
SET state = 'finished', finished_at = now(),
    count_snapshots = (SELECT count(*) FROM fetches WHERE check_id = checks.id AND outcome = 'snapshot'),
    count_failures = (SELECT count(*) FROM fetches WHERE check_id = checks.id AND outcome = 'failure'),
    count_skipped = (SELECT count(*) FROM fetches WHERE check_id = checks.id AND outcome = 'skipped')
WHERE checks.id = $1;

-- name: CheckProgress :one
SELECT
    count(*)::bigint AS known,
    count(*) FILTER (WHERE state = 'done')::bigint AS fetched,
    count(*) FILTER (WHERE outcome = 'failure')::bigint AS failures
FROM fetches WHERE check_id = $1;

-- name: ListInFlightFetches :many
SELECT f.id, f.job_id, p.url
FROM fetches f JOIN pages p ON p.id = f.page_id
WHERE f.check_id = $1 AND f.state = 'in_flight'
ORDER BY f.id;

-- name: ListFetchFailures :many
-- Fetch failures of a Check, with whether the Page also failed in the
-- Project's previous Check (the newest earlier Check that has ended).
SELECT p.url, f.failure_kind, f.message,
    EXISTS (
        SELECT 1 FROM fetches pf
        WHERE pf.page_id = f.page_id AND pf.outcome = 'failure'
          AND pf.check_id = (
              SELECT c2.id FROM checks c2
              WHERE c2.project_id = c.project_id AND c2.id < c.id
                AND c2.state IN ('finished', 'cancelled')
              ORDER BY c2.id DESC LIMIT 1)
    ) AS also_failed_previously
FROM fetches f
JOIN checks c ON c.id = f.check_id
JOIN pages p ON p.id = f.page_id
WHERE f.check_id = $1 AND f.outcome = 'failure'
ORDER BY p.url;

-- name: ListCheckRows :many
SELECT c.*,
    (SELECT count(*) FROM fetches f WHERE f.check_id = c.id)::bigint AS known,
    (SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.state = 'done')::bigint AS fetched,
    (SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.outcome = 'failure')::bigint AS failures
FROM checks c WHERE c.project_id = $1 ORDER BY c.created_at DESC, c.id DESC;

-- name: UpsertDiscoveredPages :many
-- Adds the URLs as discovered Pages of the Project; returns the ones that were new.
INSERT INTO pages (project_id, url, origin)
SELECT sqlc.arg(project_id), u, 'discovered' FROM unnest(sqlc.arg(urls)::text[]) AS u
ON CONFLICT (project_id, url) DO NOTHING
RETURNING id, url;

-- name: PageIDsByURL :many
SELECT id, url, state FROM pages
WHERE project_id = sqlc.arg(project_id) AND url = ANY(sqlc.arg(urls)::text[]);

-- name: PlanDiscoveredFetches :execrows
-- Gives every active linked Page without a Fetch in this Check a pending one.
INSERT INTO fetches (check_id, page_id)
SELECT sqlc.arg(check_id), pg.id FROM pages pg
WHERE pg.id = ANY(sqlc.arg(page_ids)::bigint[]) AND pg.state = 'active'
ORDER BY pg.id
ON CONFLICT (check_id, page_id) DO NOTHING;

-- name: InsertLinks :exec
INSERT INTO links (snapshot_id, target_page_id, source)
SELECT sqlc.arg(snapshot_id), t, sqlc.arg(source)
FROM unnest(sqlc.arg(page_ids)::bigint[]) AS t
ON CONFLICT DO NOTHING;

-- name: InsertDroppedURLs :exec
INSERT INTO dropped_urls (check_id, url, reason)
SELECT sqlc.arg(check_id), u, sqlc.arg(reason)
FROM unnest(sqlc.arg(urls)::text[]) AS u
ON CONFLICT DO NOTHING;

-- name: CountPendingFetches :one
SELECT count(*)::bigint FROM fetches WHERE check_id = $1 AND state = 'pending';

-- name: CheckDiscovery :one
-- Pages discovered during the Check and URLs it dropped.
SELECT
    (SELECT count(*) FROM pages pg
     WHERE pg.project_id = c.project_id AND pg.origin = 'discovered'
       AND c.started_at IS NOT NULL AND pg.created_at >= c.started_at)::bigint AS new_pages,
    (SELECT count(*) FROM dropped_urls d WHERE d.check_id = c.id)::bigint AS dropped
FROM checks c WHERE c.id = $1;

-- name: ListPagePool :many
-- last_status is 0 for a Page with no Snapshot yet.
SELECT pg.id, pg.url, pg.state, pg.origin, COALESCE(last.status, 0)::int AS last_status
FROM pages pg
LEFT JOIN LATERAL (
    SELECT s.status FROM snapshots s WHERE s.page_id = pg.id
    ORDER BY s.fetched_at DESC, s.id DESC LIMIT 1
) last ON true
WHERE pg.project_id = $1 ORDER BY pg.url;

-- name: ListDueProjects :many
-- Projects whose schedule is due at now: not manual, no active Check, and no
-- scheduled Check yet or the last one's creation time + interval <= now.
-- Manual Checks don't count; a cancelled scheduled Check does.
SELECT p.id FROM projects p
WHERE p.schedule <> 'manual'
  AND NOT EXISTS (
      SELECT 1 FROM checks a
      WHERE a.project_id = p.id AND a.state IN ('queued', 'running', 'cancelling'))
  AND COALESCE(
      (SELECT max(c.created_at) FROM checks c WHERE c.project_id = p.id AND c.trigger = 'scheduled')
          + CASE p.schedule
              WHEN 'hourly' THEN interval '1 hour'
              WHEN 'daily' THEN interval '1 day'
              ELSE interval '7 days'
            END,
      '-infinity'::timestamptz) <= sqlc.arg(now)::timestamptz
ORDER BY p.id;

-- name: SetCheckCreatedAt :exec
UPDATE checks SET created_at = $2 WHERE id = $1;
