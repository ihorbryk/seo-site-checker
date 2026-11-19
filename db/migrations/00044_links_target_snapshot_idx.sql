-- +goose NO TRANSACTION
-- +goose Up
-- FinalizeCheck asks whether a Snapshot of the Check links to a Page. With the
-- Snapshot id in the index that reads only the links from the Check's own
-- Snapshots (ids from checks.first_fetch_id on), not every link to the Page
-- from every earlier Check. Built concurrently, dropping any INVALID leftover
-- of an interrupted run first (see 00042).
DROP INDEX CONCURRENTLY IF EXISTS links_target_snapshot_idx;
CREATE INDEX CONCURRENTLY links_target_snapshot_idx ON links (target_page_id, snapshot_id);
DROP INDEX CONCURRENTLY IF EXISTS links_target_idx;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS links_target_idx;
CREATE INDEX CONCURRENTLY links_target_idx ON links (target_page_id);
DROP INDEX CONCURRENTLY IF EXISTS links_target_snapshot_idx;
