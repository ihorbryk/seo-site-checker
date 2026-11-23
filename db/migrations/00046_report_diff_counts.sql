-- +goose Up
-- A Report is fixed when its Check ends (ADR 0008): FinalizeCheck stores how
-- many Diffs the Check has in each tier, and whether its seed Page failed,
-- next to the other Report counts (ADR 0007). Changed is the sum of the four
-- non-empty tiers, Unchanged is count_diff_empty.
ALTER TABLE checks
    ADD COLUMN count_diff_critical integer NOT NULL DEFAULT 0,
    ADD COLUMN count_diff_seo integer NOT NULL DEFAULT 0,
    ADD COLUMN count_diff_headers integer NOT NULL DEFAULT 0,
    ADD COLUMN count_diff_body integer NOT NULL DEFAULT 0,
    ADD COLUMN count_diff_empty integer NOT NULL DEFAULT 0,
    ADD COLUMN seed_failed boolean NOT NULL DEFAULT false;

-- Backfill every Check that has ended by counting all its Diffs, and a Check
-- partway through FinalizeCheck for the Pages it has already finalized (ids
-- up to finalize_cursor): its remaining batches add the rest. A Check whose
-- finalizing has not begun is left at zero for the same reason.
UPDATE checks c SET
    count_diff_critical = d.critical, count_diff_seo = d.seo, count_diff_headers = d.headers,
    count_diff_body = d.body, count_diff_empty = d.empty
FROM (
    SELECT c2.id,
        count(*) FILTER (WHERE df.tier = 'critical')::int AS critical,
        count(*) FILTER (WHERE df.tier = 'seo')::int AS seo,
        count(*) FILTER (WHERE df.tier = 'headers')::int AS headers,
        count(*) FILTER (WHERE df.tier = 'body')::int AS body,
        count(*) FILTER (WHERE df.tier = 'empty')::int AS empty
    FROM checks c2
    JOIN snapshots s ON s.check_id = c2.id
    JOIN diffs df ON df.snapshot_id = s.id
    WHERE c2.state IN ('finished', 'cancelled') OR s.page_id <= c2.finalize_cursor
    GROUP BY c2.id
) d
WHERE c.id = d.id;

UPDATE checks c SET seed_failed = true
WHERE (c.state IN ('finished', 'cancelled') OR c.finalize_cursor IS NOT NULL)
  AND EXISTS (SELECT 1 FROM fetches f JOIN pages g ON g.id = f.page_id
              WHERE f.check_id = c.id AND g.origin = 'seed'
                AND (c.state IN ('finished', 'cancelled') OR g.id <= c.finalize_cursor)
                AND (f.outcome = 'failure' OR (f.outcome = 'skipped' AND f.skip_reason <> 'cancelled')));

-- +goose Down
ALTER TABLE checks
    DROP COLUMN count_diff_critical,
    DROP COLUMN count_diff_seo,
    DROP COLUMN count_diff_headers,
    DROP COLUMN count_diff_body,
    DROP COLUMN count_diff_empty,
    DROP COLUMN seed_failed;
