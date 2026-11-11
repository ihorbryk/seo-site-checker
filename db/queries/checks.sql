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
SET state = sqlc.arg(state), finished_at = now(),
    count_snapshots = (SELECT count(*) FROM fetches WHERE check_id = checks.id AND outcome = 'snapshot'),
    count_failures = (SELECT count(*) FROM fetches WHERE check_id = checks.id AND outcome = 'failure'),
    count_skipped = (SELECT count(*) FROM fetches WHERE check_id = checks.id AND outcome = 'skipped')
WHERE checks.id = sqlc.arg(id);

-- name: RequestCancel :execrows
-- A queued Check has nothing to wind down and is cancelled at once; a running
-- one becomes `cancelling` and its lanes stop claiming Fetches.
UPDATE checks
SET state = CASE WHEN state = 'queued' THEN 'cancelled' ELSE 'cancelling' END,
    finished_at = CASE WHEN state = 'queued' THEN now() END
WHERE id = $1 AND state IN ('queued', 'running');

-- name: SkipPendingFetches :exec
UPDATE fetches SET state = 'done', outcome = 'skipped', skip_reason = 'cancelled'
WHERE check_id = $1 AND state = 'pending';

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

-- name: CountChecks :one
SELECT count(*)::bigint FROM checks WHERE project_id = $1;

-- name: ListCheckRows :many
-- One page of the Checks tab: Checks newest first with their progress and,
-- once ended, the Report counts. Changed counts leave out Pages now ignored.
SELECT c.*,
    (SELECT count(*) FROM fetches f WHERE f.check_id = c.id)::bigint AS known,
    (SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.state = 'done')::bigint AS fetched,
    (SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.outcome = 'failure')::bigint AS failures,
    (SELECT count(*) FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id JOIN pages g ON g.id = s.page_id
     WHERE s.check_id = c.id AND d.tier <> 'empty' AND g.state <> 'ignored')::bigint AS changed,
    (SELECT count(*) FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id JOIN pages g ON g.id = s.page_id
     WHERE s.check_id = c.id AND d.tier = 'critical' AND g.state <> 'ignored')::bigint AS critical,
    (SELECT count(*) FROM diffs d JOIN snapshots s ON s.id = d.snapshot_id JOIN pages g ON g.id = s.page_id
     WHERE s.check_id = c.id AND d.tier = 'seo' AND g.state <> 'ignored')::bigint AS seo,
    EXISTS (SELECT 1 FROM fetches f JOIN pages g ON g.id = f.page_id
            WHERE f.check_id = c.id AND g.origin = 'seed' AND (f.outcome = 'failure' OR (f.outcome = 'skipped' AND f.skip_reason <> 'cancelled'))) AS seed_failed
FROM checks c WHERE c.project_id = sqlc.arg(project_id)
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountNewlyDiscoveredPages :one
-- Pages the Check's own discovery added to the pool.
SELECT count(*)::bigint FROM pages pg JOIN checks c ON c.id = $1
WHERE pg.project_id = c.project_id AND pg.origin = 'discovered' AND pg.created_at >= c.started_at;

-- name: UpsertDiscoveredPages :exec
-- Adds the URLs as discovered Pages of the Project, leaving existing ones alone.
INSERT INTO pages (project_id, url, origin)
SELECT sqlc.arg(project_id), u, 'discovered' FROM unnest(sqlc.arg(urls)::text[]) AS u
ON CONFLICT (project_id, url) DO NOTHING;

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

-- name: CountDroppedURLs :one
-- URLs the Check dropped under the sanity limits.
SELECT count(*)::bigint FROM dropped_urls WHERE check_id = $1;

-- name: ReactivateLinkedPages :exec
-- Brings retired Pages that a Snapshot linked to back into the pool, with a
-- fresh orphan streak, and remembers the Check; FinalizeCheck records the
-- `reactivated` events.
UPDATE pages SET state = 'active', orphan_streak = 0, reactivated_check_id = sqlc.arg(check_id)
WHERE id = ANY(sqlc.arg(page_ids)::bigint[]) AND state = 'retired';

-- name: InsertPageEvents :exec
INSERT INTO page_events (check_id, page_id, kind)
SELECT sqlc.arg(check_id), p, sqlc.arg(kind) FROM unnest(sqlc.arg(page_ids)::bigint[]) AS p
ON CONFLICT DO NOTHING;

-- name: InsertNewPageEvents :exec
-- `new`: a Page that got its first Snapshot in this Check (the seed Page is
-- the Project's starting point, not a find).
INSERT INTO page_events (check_id, page_id, kind)
SELECT sqlc.arg(check_id), s.page_id, 'new'
FROM snapshots s JOIN pages pg ON pg.id = s.page_id
WHERE s.check_id = sqlc.arg(check_id) AND pg.origin <> 'seed'
  AND NOT EXISTS (SELECT 1 FROM snapshots o WHERE o.page_id = s.page_id AND o.check_id < s.check_id)
ON CONFLICT DO NOTHING;

-- name: InsertReactivatedPageEvents :exec
INSERT INTO page_events (check_id, page_id, kind)
SELECT sqlc.arg(check_id), pg.id, 'reactivated' FROM pages pg
WHERE pg.reactivated_check_id = sqlc.arg(check_id)
ON CONFLICT DO NOTHING;

-- name: SeedFetchOutcome :one
-- The outcome of the seed Page's Fetch in the Check; NULL until it is done.
SELECT f.outcome FROM fetches f JOIN pages pg ON pg.id = f.page_id
WHERE f.check_id = $1 AND pg.origin = 'seed';

-- name: MarkOrphanCountingSkipped :exec
UPDATE checks SET orphan_counting_skipped = true WHERE id = $1;

-- name: ResetLinkedOrphanStreaks :exec
-- Pages some other Page's Snapshot in the Check linked to are not orphaned. A
-- Page skipped in the Check sent no request, so its links come from its latest
-- earlier Snapshot.
UPDATE pages pg SET orphan_streak = 0
WHERE pg.orphan_streak > 0 AND pg.state = 'active' AND (
    EXISTS (
        SELECT 1 FROM links l JOIN snapshots s ON s.id = l.snapshot_id
        WHERE s.check_id = sqlc.arg(check_id) AND l.target_page_id = pg.id AND s.page_id <> pg.id)
    OR EXISTS (
        SELECT 1 FROM fetches sf
        JOIN LATERAL (SELECT s.id FROM snapshots s WHERE s.page_id = sf.page_id
                      ORDER BY s.fetched_at DESC, s.id DESC LIMIT 1) last ON true
        JOIN links l ON l.snapshot_id = last.id
        WHERE sf.check_id = sqlc.arg(check_id) AND sf.outcome = 'skipped'
          AND sf.page_id <> pg.id AND l.target_page_id = pg.id));

-- name: CountOrphans :many
-- Orphaned Pages (discovered or manual, active, fetched in the Check without a
-- Fetch failure, linked by no other Page's Snapshot) get their streak raised;
-- the third orphaned Check retires a discovered Page. A manual Page's streak
-- stops at 2: it vanishes but is never retired. The seed Page is never
-- counted. Returns each orphaned Page's new streak.
UPDATE pages pg
SET orphan_streak = CASE WHEN pg.origin = 'manual' THEN least(pg.orphan_streak + 1, 2) ELSE pg.orphan_streak + 1 END,
    state = CASE WHEN pg.origin = 'discovered' AND pg.orphan_streak + 1 >= 3 THEN 'retired' ELSE pg.state END
WHERE pg.state = 'active' AND pg.origin IN ('discovered', 'manual')
  AND EXISTS (SELECT 1 FROM fetches f WHERE f.check_id = sqlc.arg(check_id) AND f.page_id = pg.id AND f.outcome <> 'failure')
  AND NOT EXISTS (
      SELECT 1 FROM links l JOIN snapshots s ON s.id = l.snapshot_id
      WHERE s.check_id = sqlc.arg(check_id) AND l.target_page_id = pg.id AND s.page_id <> pg.id)
  AND NOT EXISTS (
      SELECT 1 FROM fetches sf
      JOIN LATERAL (SELECT s.id FROM snapshots s WHERE s.page_id = sf.page_id
                    ORDER BY s.fetched_at DESC, s.id DESC LIMIT 1) last ON true
      JOIN links l ON l.snapshot_id = last.id
      WHERE sf.check_id = sqlc.arg(check_id) AND sf.outcome = 'skipped'
        AND sf.page_id <> pg.id AND l.target_page_id = pg.id)
RETURNING pg.id, pg.orphan_streak;

-- name: SetCheckEventCounts :exec
UPDATE checks SET
    count_new = (SELECT count(*) FROM page_events e WHERE e.check_id = checks.id AND e.kind IN ('new', 'reactivated')),
    count_vanished = (SELECT count(*) FROM page_events e WHERE e.check_id = checks.id AND e.kind = 'vanished'),
    count_retired = (SELECT count(*) FROM page_events e WHERE e.check_id = checks.id AND e.kind = 'retired')
WHERE id = $1;

-- name: ListPageEvents :many
-- The Pages with the event kind in the Check (new includes reactivated),
-- leaving out Pages now ignored.
SELECT pg.url, e.kind FROM page_events e JOIN pages pg ON pg.id = e.page_id
WHERE e.check_id = $1 AND pg.state <> 'ignored' AND e.kind = ANY(sqlc.arg(kinds)::text[])
ORDER BY pg.url;

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

-- name: SetCheckRobots :exec
-- Stores the robots.txt the Check fetched; the status is NULL after a transport error.
UPDATE checks
SET robots_status = sqlc.narg(status), robots_body = sqlc.arg(body),
    robots_fetched_at = now(), robots_failure_kind = sqlc.narg(failure_kind)
WHERE id = sqlc.arg(id);

-- name: FinishFetchSkipped :exec
UPDATE fetches SET state = 'done', outcome = 'skipped', skip_reason = $2 WHERE id = $1;

-- name: ListSkippedFetches :many
SELECT p.url, f.skip_reason FROM fetches f JOIN pages p ON p.id = f.page_id
WHERE f.check_id = $1 AND f.outcome = 'skipped'
ORDER BY p.url;
