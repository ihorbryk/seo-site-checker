-- +goose Up
-- FinalizeCheck works through a Check's Pages in batches (ADR 0007).
-- finalize_cursor is the id of the last Page finalized, NULL until the first
-- batch: a Check whose finalizing has begun can no longer be cancelled.
-- first_fetch_id is the Check's lowest Fetch id: every Snapshot of the Check
-- has an id at or above it, every earlier Check's Snapshots one below it.
ALTER TABLE checks
    ADD COLUMN finalize_cursor bigint,
    ADD COLUMN first_fetch_id bigint;

-- +goose Down
-- The old FinalizeCheck finalizes in one go: restart the Checks caught
-- mid-finalize. Their events so far are dropped and orphan streaks already
-- changed stay changed (the old job raises them again: at worst a Page
-- vanishes or retires one Check early).
DELETE FROM page_events WHERE check_id IN (SELECT id FROM checks WHERE finalize_cursor IS NOT NULL AND finished_at IS NULL);
ALTER TABLE checks
    DROP COLUMN finalize_cursor,
    DROP COLUMN first_fetch_id;
