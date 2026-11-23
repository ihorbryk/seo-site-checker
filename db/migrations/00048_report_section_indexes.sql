-- +goose NO TRANSACTION
-- +goose Up
-- A page of a Report section costs about a page of work (#78). A section that
-- is dense next to the Project's Pages is read by walking the Pages in URL
-- order and probing each for a row of the Check; a sparse one is read through
-- an index that holds only its rows. These are the indexes of the sparse way.
--
-- Built concurrently: fetches, diffs and page_events hold millions of rows on
-- a large deployment. Each statement commits on its own (NO TRANSACTION), so a
-- rerun after an interrupted build first drops the INVALID index that build
-- left behind (see 00042).

-- A Diff stores its Check, so the Diffs of one tier of one Check can be found
-- without going through every Snapshot of the Check. No foreign key: a Diff is
-- deleted with its Snapshot, which is deleted with the Check.
ALTER TABLE diffs ADD COLUMN IF NOT EXISTS check_id bigint;
UPDATE diffs d SET check_id = s.check_id FROM snapshots s WHERE s.id = d.snapshot_id AND d.check_id IS NULL;
ALTER TABLE diffs ALTER COLUMN check_id SET NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS diffs_check_tier_idx;
CREATE INDEX CONCURRENTLY diffs_check_tier_idx ON diffs (check_id, tier, snapshot_id) WHERE tier <> 'empty';
DROP INDEX CONCURRENTLY IF EXISTS fetches_check_outcome_idx;
CREATE INDEX CONCURRENTLY fetches_check_outcome_idx ON fetches (check_id, outcome, page_id)
    WHERE outcome IN ('failure', 'skipped');
DROP INDEX CONCURRENTLY IF EXISTS page_events_check_kind_idx;
CREATE INDEX CONCURRENTLY page_events_check_kind_idx ON page_events (check_id, kind, page_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS page_events_check_kind_idx;
DROP INDEX CONCURRENTLY IF EXISTS fetches_check_outcome_idx;
DROP INDEX CONCURRENTLY IF EXISTS diffs_check_tier_idx;
ALTER TABLE diffs DROP COLUMN IF EXISTS check_id;
