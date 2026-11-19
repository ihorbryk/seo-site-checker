-- +goose NO TRANSACTION
-- +goose Up
-- Built concurrently: fetches and pages hold millions of rows on a large
-- deployment, and a plain CREATE INDEX would block their writes meanwhile.
-- Each statement commits on its own, so a rerun after an interrupted build
-- first drops the INVALID index that build left behind, and an old index is
-- dropped only once its replacement is built.
--
-- A lane claims its Check's lowest pending Fetch id. With the id in the index
-- the claim reads one entry instead of walking the Check's done Fetches
-- (213 ms halfway through a 2M-Fetch Check before, 1 ms after).
DROP INDEX CONCURRENTLY IF EXISTS fetches_pending_check_id_idx;
CREATE INDEX CONCURRENTLY fetches_pending_check_id_idx ON fetches (check_id, id)
    WHERE state = 'pending';
DROP INDEX CONCURRENTLY IF EXISTS fetches_pending_idx;
-- StartCheck reads a Project's active Pages in id order, one batch at a time.
DROP INDEX CONCURRENTLY IF EXISTS pages_project_state_id_idx;
CREATE INDEX CONCURRENTLY pages_project_state_id_idx ON pages (project_id, state, id);
DROP INDEX CONCURRENTLY IF EXISTS pages_project_state_idx;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS pages_project_state_idx;
CREATE INDEX CONCURRENTLY pages_project_state_idx ON pages (project_id, state);
DROP INDEX CONCURRENTLY IF EXISTS pages_project_state_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS fetches_pending_idx;
CREATE INDEX CONCURRENTLY fetches_pending_idx ON fetches (check_id) WHERE state = 'pending';
DROP INDEX CONCURRENTLY IF EXISTS fetches_pending_check_id_idx;
