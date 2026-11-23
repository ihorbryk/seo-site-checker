-- name: CreateCheck :one
-- Inserts a queued Check with the Project's next Check number. Raising the
-- counter locks the Project row until the transaction ends, so concurrent
-- creations in one Project get consecutive numbers (ADR 0001).
WITH n AS (
    UPDATE projects SET last_check_number = last_check_number + 1
    WHERE id = sqlc.arg(project_id)
    RETURNING last_check_number
)
INSERT INTO checks (project_id, trigger, number)
SELECT sqlc.arg(project_id), sqlc.arg(trigger), n.last_check_number FROM n
RETURNING *;

-- name: GetCheck :one
SELECT * FROM checks WHERE id = $1;

-- name: GetCheckByNumber :one
SELECT * FROM checks WHERE project_id = $1 AND number = $2;

-- name: LockCheck :one
SELECT * FROM checks WHERE id = $1 FOR UPDATE;

-- name: GetActiveCheck :one
SELECT * FROM checks
WHERE project_id = $1 AND state IN ('queued', 'planning', 'running', 'cancelling');

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1;

-- name: BeginPlanning :exec
-- Starts a queued Check: it freezes the Request settings and Noise rules and
-- plans its Fetches from the first Page on (ADR 0006).
UPDATE checks
SET state = 'planning', started_at = now(), settings = $2, noise_rules = $3, plan_cursor = 0
WHERE id = $1;

-- name: ListPlanBatch :many
-- The next batch of a Project's active Pages to plan, in id order after the
-- cursor (pages_project_state_id_idx).
SELECT id, url, origin FROM pages
WHERE project_id = sqlc.arg(project_id) AND state = 'active' AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(batch_size);

-- name: PlanFetches :execrows
INSERT INTO fetches (check_id, page_id)
SELECT sqlc.arg(check_id), pg.id FROM pages pg
WHERE pg.project_id = sqlc.arg(project_id) AND pg.state = 'active'
  AND pg.id = ANY(sqlc.arg(page_ids)::bigint[])
ORDER BY pg.id;

-- name: SetPlanCursor :exec
UPDATE checks SET plan_cursor = $2 WHERE id = $1;

-- name: MarkCheckRunning :exec
-- Ends planning: the Check runs `lanes` lanes.
UPDATE checks SET state = 'running', lanes = $2 WHERE id = $1;

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

-- name: RequestCancel :execrows
-- A queued Check has nothing to wind down and is cancelled at once. A planning
-- one becomes `cancelling` and stops planning; a running one becomes
-- `cancelling` and its lanes stop claiming Fetches. Once FinalizeCheck has
-- begun (ADR 0007) every Fetch is done and the Check can't be cancelled.
UPDATE checks
SET state = CASE WHEN state = 'queued' THEN 'cancelled' ELSE 'cancelling' END,
    finished_at = CASE WHEN state = 'queued' THEN now() END
WHERE id = $1 AND state IN ('queued', 'planning', 'running') AND finalize_cursor IS NULL;

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
-- One page of the fetch failures of a Check, by URL, with whether the Page also failed in the
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
ORDER BY p.url
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountFetchFailures :one
SELECT count(*)::bigint FROM fetches WHERE check_id = $1 AND outcome = 'failure';

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
-- excluded[i] is the answer for urls[i] under the Project's current patterns.
INSERT INTO pages (project_id, url, origin, excluded)
SELECT sqlc.arg(project_id), u.url, 'discovered', f.excluded
FROM unnest(sqlc.arg(urls)::text[]) WITH ORDINALITY AS u(url, n)
JOIN unnest(sqlc.arg(excluded)::boolean[]) WITH ORDINALITY AS f(excluded, n) USING (n)
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

-- name: MarkOrphanCountingSkipped :exec
UPDATE checks SET orphan_counting_skipped = true WHERE id = $1;

-- name: ListPageEvents :many
-- One page of the Pages with the event kind in the Check (new includes
-- reactivated), by URL, leaving out Pages now ignored.
SELECT pg.url, e.kind FROM page_events e JOIN pages pg ON pg.id = e.page_id
WHERE e.check_id = $1 AND pg.state <> 'ignored' AND e.kind = ANY(sqlc.arg(kinds)::text[])
ORDER BY pg.url, e.kind
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountPageEvents :one
-- How many rows ListPageEvents has across all its pages.
SELECT count(*)::bigint FROM page_events e JOIN pages pg ON pg.id = e.page_id
WHERE e.check_id = $1 AND pg.state <> 'ignored' AND e.kind = ANY(sqlc.arg(kinds)::text[]);

-- name: ListDueProjects :many
-- Projects whose schedule is due at now: not manual, no active Check, and no
-- scheduled Check yet or the last one's creation time + interval <= now.
-- Manual Checks don't count; a cancelled scheduled Check does.
SELECT p.id FROM projects p
WHERE p.schedule <> 'manual'
  AND NOT EXISTS (
      SELECT 1 FROM checks a
      WHERE a.project_id = p.id AND a.state IN ('queued', 'planning', 'running', 'cancelling'))
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
-- One page of a Check's skipped Fetches, by URL.
SELECT p.url, f.skip_reason FROM fetches f JOIN pages p ON p.id = f.page_id
WHERE f.check_id = $1 AND f.outcome = 'skipped'
ORDER BY p.url
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountSkippedFetches :one
SELECT count(*)::bigint FROM fetches WHERE check_id = $1 AND outcome = 'skipped';

-- FinalizeCheck (ADR 0007) works through a Check's Pages in batches by id:
-- each batch is the Pages with ids in (after_id, upto_id], bounded by the
-- page_ids of the Check's Fetches so it holds at most a batch of them.

-- name: BeginFinalize :exec
-- Zeroes the Report counts the batches add up and records the Check's lowest
-- Fetch id (see 00043_finalize_batches.sql).
UPDATE checks SET finalize_cursor = 0,
    first_fetch_id = (SELECT min(f.id) FROM fetches f WHERE f.check_id = checks.id),
    count_snapshots = 0, count_failures = 0, count_skipped = 0,
    count_new = 0, count_vanished = 0, count_retired = 0
WHERE checks.id = $1;

-- name: FinalizeBatchEnd :one
-- The page_id of the Check's batch_size-th Fetch after after_id, by page_id;
-- no row when fewer are left, and the batch is the last one.
SELECT page_id FROM fetches
WHERE check_id = sqlc.arg(check_id) AND page_id > sqlc.arg(after_id)
ORDER BY page_id
OFFSET sqlc.arg(batch_size)::bigint - 1 LIMIT 1;

-- name: SeedFetchOutcome :one
-- The outcome of the seed Page's Fetch in the Check; NULL until it is done.
SELECT f.outcome FROM fetches f JOIN pages pg ON pg.id = f.page_id
WHERE f.check_id = $1 AND pg.origin = 'seed';

-- name: SkipPendingFetches :exec
UPDATE fetches SET state = 'done', outcome = 'skipped', skip_reason = 'cancelled'
WHERE check_id = sqlc.arg(check_id) AND page_id > sqlc.arg(after_id) AND page_id <= sqlc.arg(upto_id)
  AND state = 'pending';

-- name: ResetLinkedOrphanStreaks :exec
-- Pages some other Page's Snapshot in the Check linked to are not orphaned. A
-- Page skipped in the Check sent no request, so its links come from its latest
-- earlier Snapshot. Each Page's links are read through
-- links_target_snapshot_idx: from the Check's own Snapshots (ids from
-- first_fetch_id on), then, only if none, from earlier ones. The CASE and the
-- OFFSET 0 keep the planner from turning the lookups into joins over all the
-- Check's links (4.7 s instead of 38 ms per batch of 10,000 Pages).
UPDATE pages pg SET orphan_streak = 0
WHERE pg.project_id = sqlc.arg(project_id) AND pg.state = 'active'
  AND pg.id > sqlc.arg(after_id) AND pg.id <= sqlc.arg(upto_id) AND pg.orphan_streak > 0
  AND CASE WHEN EXISTS (
          SELECT 1 FROM links l WHERE l.target_page_id = pg.id AND l.snapshot_id >= sqlc.arg(first_fetch_id)
            AND EXISTS (SELECT 1 FROM snapshots s
                        WHERE s.id = l.snapshot_id AND s.check_id = sqlc.arg(check_id) AND s.page_id <> pg.id
                        OFFSET 0))
      THEN true
      ELSE EXISTS (
          SELECT 1 FROM links l WHERE l.target_page_id = pg.id AND l.snapshot_id < sqlc.arg(first_fetch_id)
            AND EXISTS (SELECT 1 FROM snapshots s
                        JOIN fetches sf ON sf.check_id = sqlc.arg(check_id) AND sf.page_id = s.page_id AND sf.outcome = 'skipped'
                        WHERE s.id = l.snapshot_id AND s.page_id <> pg.id
                          AND s.id = (SELECT s2.id FROM snapshots s2 WHERE s2.page_id = s.page_id
                                      ORDER BY s2.fetched_at DESC, s2.id DESC LIMIT 1)
                        OFFSET 0))
      END;

-- name: CountOrphans :many
-- Orphaned Pages (discovered or manual, active, fetched in the Check without a
-- Fetch failure, linked by no other Page's Snapshot, as ResetLinkedOrphanStreaks
-- reads links) get their streak raised; the third orphaned Check retires a
-- discovered Page. A manual Page's streak stops at 2: it vanishes but is never
-- retired. The seed Page is never counted. Returns each orphaned Page's new
-- streak.
UPDATE pages pg
SET orphan_streak = CASE WHEN pg.origin = 'manual' THEN least(pg.orphan_streak + 1, 2) ELSE pg.orphan_streak + 1 END,
    state = CASE WHEN pg.origin = 'discovered' AND pg.orphan_streak + 1 >= 3 THEN 'retired' ELSE pg.state END
WHERE pg.project_id = sqlc.arg(project_id) AND pg.state = 'active'
  AND pg.id > sqlc.arg(after_id) AND pg.id <= sqlc.arg(upto_id) AND pg.origin IN ('discovered', 'manual')
  AND EXISTS (SELECT 1 FROM fetches f WHERE f.check_id = sqlc.arg(check_id) AND f.page_id = pg.id AND f.outcome <> 'failure')
  AND CASE WHEN EXISTS (
          SELECT 1 FROM links l WHERE l.target_page_id = pg.id AND l.snapshot_id >= sqlc.arg(first_fetch_id)
            AND EXISTS (SELECT 1 FROM snapshots s
                        WHERE s.id = l.snapshot_id AND s.check_id = sqlc.arg(check_id) AND s.page_id <> pg.id
                        OFFSET 0))
      THEN false
      ELSE NOT EXISTS (
          SELECT 1 FROM links l WHERE l.target_page_id = pg.id AND l.snapshot_id < sqlc.arg(first_fetch_id)
            AND EXISTS (SELECT 1 FROM snapshots s
                        JOIN fetches sf ON sf.check_id = sqlc.arg(check_id) AND sf.page_id = s.page_id AND sf.outcome = 'skipped'
                        WHERE s.id = l.snapshot_id AND s.page_id <> pg.id
                          AND s.id = (SELECT s2.id FROM snapshots s2 WHERE s2.page_id = s.page_id
                                      ORDER BY s2.fetched_at DESC, s2.id DESC LIMIT 1)
                        OFFSET 0))
      END
RETURNING pg.id, pg.orphan_streak;

-- name: InsertNewPageEvents :exec
-- `new`: a Page that got its first Snapshot in this Check (the seed Page is
-- the Project's starting point, not a find).
INSERT INTO page_events (check_id, page_id, kind)
SELECT sqlc.arg(check_id), f.page_id, 'new'
FROM fetches f JOIN pages pg ON pg.id = f.page_id
WHERE f.check_id = sqlc.arg(check_id) AND f.page_id > sqlc.arg(after_id) AND f.page_id <= sqlc.arg(upto_id)
  AND f.outcome = 'snapshot' AND pg.origin <> 'seed'
  AND NOT EXISTS (SELECT 1 FROM snapshots o WHERE o.page_id = f.page_id AND o.check_id < sqlc.arg(check_id))
ON CONFLICT DO NOTHING;

-- name: InsertReactivatedPageEvents :exec
-- A Page discovery brought back in this Check always got a Fetch in it:
-- addLinkedPages reactivates linked Pages and then plans a Fetch for each of
-- them, in the same transaction.
INSERT INTO page_events (check_id, page_id, kind)
SELECT sqlc.arg(check_id), pg.id, 'reactivated'
FROM fetches f JOIN pages pg ON pg.id = f.page_id
WHERE f.check_id = sqlc.arg(check_id) AND f.page_id > sqlc.arg(after_id) AND f.page_id <= sqlc.arg(upto_id)
  AND pg.reactivated_check_id = sqlc.arg(check_id)
ON CONFLICT DO NOTHING;

-- name: AddFinalizeBatchCounts :exec
-- Adds the batch's Fetch outcomes and page events to the Report counts and
-- moves the cursor past it.
UPDATE checks SET finalize_cursor = sqlc.arg(upto_id)::bigint,
    count_snapshots = count_snapshots + b.snapshots, count_failures = count_failures + b.failures,
    count_skipped = count_skipped + b.skipped,
    count_new = count_new + e.new, count_vanished = count_vanished + e.vanished, count_retired = count_retired + e.retired
FROM (
    SELECT count(*) FILTER (WHERE bf.outcome = 'snapshot')::int AS snapshots,
           count(*) FILTER (WHERE bf.outcome = 'failure')::int AS failures,
           count(*) FILTER (WHERE bf.outcome = 'skipped')::int AS skipped
    FROM fetches bf
    WHERE bf.check_id = sqlc.arg(check_id) AND bf.page_id > sqlc.arg(after_id) AND bf.page_id <= sqlc.arg(upto_id)
) b, (
    SELECT count(*) FILTER (WHERE be.kind IN ('new', 'reactivated'))::int AS new,
           count(*) FILTER (WHERE be.kind = 'vanished')::int AS vanished,
           count(*) FILTER (WHERE be.kind = 'retired')::int AS retired
    FROM page_events be
    WHERE be.check_id = sqlc.arg(check_id) AND be.page_id > sqlc.arg(after_id) AND be.page_id <= sqlc.arg(upto_id)
) e
WHERE checks.id = sqlc.arg(check_id);

-- name: EndCheck :exec
UPDATE checks SET state = sqlc.arg(state), finished_at = now() WHERE id = sqlc.arg(id);
