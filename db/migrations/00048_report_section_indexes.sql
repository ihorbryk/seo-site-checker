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
--
-- `docker compose up -d` runs this while the old worker is still running, and
-- its Diff insert does not name check_id. A BEFORE INSERT trigger fills the
-- column from the Snapshot when it is NULL, so those inserts keep working and
-- are correct. The trigger stays: the new code always sets check_id, so it
-- costs one skipped branch; drop it in a later migration once no old worker
-- can run.
ALTER TABLE diffs ADD COLUMN IF NOT EXISTS check_id bigint;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION diffs_fill_check_id() RETURNS trigger AS $$
BEGIN
    IF NEW.check_id IS NULL THEN
        SELECT check_id INTO NEW.check_id FROM snapshots WHERE id = NEW.snapshot_id;
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS diffs_fill_check_id ON diffs;
CREATE TRIGGER diffs_fill_check_id BEFORE INSERT ON diffs
    FOR EACH ROW EXECUTE FUNCTION diffs_fill_check_id();

-- Backfill in batches of 10,000 Diffs by snapshot_id, each committed on its
-- own (the DO block commits because this migration runs outside a
-- transaction), so no single statement holds locks or WAL for every Diff. A
-- rerun resumes: only Diffs without a Check are touched.
-- +goose StatementBegin
DO $$
DECLARE
    last_id bigint := 0;
    next_id bigint;
BEGIN
    LOOP
        SELECT max(snapshot_id) INTO next_id FROM (
            SELECT snapshot_id FROM diffs WHERE snapshot_id > last_id ORDER BY snapshot_id LIMIT 10000
        ) b;
        EXIT WHEN next_id IS NULL;
        UPDATE diffs d SET check_id = s.check_id FROM snapshots s
        WHERE s.id = d.snapshot_id AND d.snapshot_id > last_id AND d.snapshot_id <= next_id AND d.check_id IS NULL;
        last_id := next_id;
        COMMIT;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- SET NOT NULL scans the table under an exclusive lock unless a validated
-- CHECK already proves it; validating takes only a weak lock.
ALTER TABLE diffs ADD CONSTRAINT diffs_check_id_present CHECK (check_id IS NOT NULL) NOT VALID;
ALTER TABLE diffs VALIDATE CONSTRAINT diffs_check_id_present;
ALTER TABLE diffs ALTER COLUMN check_id SET NOT NULL;
ALTER TABLE diffs DROP CONSTRAINT diffs_check_id_present;

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
DROP TRIGGER IF EXISTS diffs_fill_check_id ON diffs;
DROP FUNCTION IF EXISTS diffs_fill_check_id();
ALTER TABLE diffs DROP COLUMN IF EXISTS check_id;
