-- +goose NO TRANSACTION
-- +goose Up
-- A Page stores whether one of its Project's exclude patterns matches it
-- (ADR 0009), so the Page pool tab can filter, count and paginate in SQL. Go
-- stays the only place a pattern is matched; a recompute job fills the column.
--
-- Plain ADD COLUMN with a constant default is a catalog-only change in
-- Postgres 11+, so these statements are quick on a large pages table. Each
-- statement commits on its own (NO TRANSACTION) because the indexes are built
-- concurrently; a rerun after an interrupted build first drops the INVALID
-- index that build left behind (see 00042).
ALTER TABLE pages ADD COLUMN IF NOT EXISTS excluded boolean NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS exclude_gen bigint NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS exclude_cursor bigint NOT NULL DEFAULT 0;
-- A recompute of generation exclude_gen is unfinished while exclude_done_gen
-- is behind it; the worker catches it up after the last batch.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS exclude_done_gen bigint NOT NULL DEFAULT 0;

-- One partial index per Page pool filter, in URL order. "All" is served by
-- the (project_id, url) unique index.
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_active_idx;
CREATE INDEX CONCURRENTLY pages_pool_active_idx ON pages (project_id, url)
    WHERE state = 'active' AND NOT excluded;
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_orphaned_idx;
CREATE INDEX CONCURRENTLY pages_pool_orphaned_idx ON pages (project_id, url)
    WHERE state = 'active' AND NOT excluded AND orphan_streak > 0;
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_retired_idx;
CREATE INDEX CONCURRENTLY pages_pool_retired_idx ON pages (project_id, url) WHERE state = 'retired';
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_ignored_idx;
CREATE INDEX CONCURRENTLY pages_pool_ignored_idx ON pages (project_id, url) WHERE state = 'ignored';
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_excluded_idx;
CREATE INDEX CONCURRENTLY pages_pool_excluded_idx ON pages (project_id, url)
    WHERE state = 'active' AND excluded;
-- The recompute reads a Project's Pages in id order, one batch at a time.
DROP INDEX CONCURRENTLY IF EXISTS pages_project_id_idx;
CREATE INDEX CONCURRENTLY pages_project_id_idx ON pages (project_id, id);

-- Pages that exist now have no answer yet. A Project with no exclude patterns
-- is already right (nothing matches); every other Project is marked as having
-- an unfinished recompute (generation 1, done generation 0), and the scheduler
-- queues its job. The migration inserts no job itself: it runs while the old
-- worker is still up, which would fail the unknown kind until River discards
-- it, and the Pages would stay unclassified. The mark survives that.
UPDATE projects SET exclude_gen = 1, exclude_cursor = 0
WHERE exclude_gen = 0 AND cardinality(exclude_patterns) > 0;

-- +goose Down
DELETE FROM river_job WHERE kind = 'exclude_recompute' AND state IN ('available', 'scheduled', 'retryable');
DROP INDEX CONCURRENTLY IF EXISTS pages_project_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_excluded_idx;
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_ignored_idx;
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_retired_idx;
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_orphaned_idx;
DROP INDEX CONCURRENTLY IF EXISTS pages_pool_active_idx;
ALTER TABLE projects DROP COLUMN IF EXISTS exclude_done_gen;
ALTER TABLE projects DROP COLUMN IF EXISTS exclude_cursor;
ALTER TABLE projects DROP COLUMN IF EXISTS exclude_gen;
ALTER TABLE pages DROP COLUMN IF EXISTS excluded;
