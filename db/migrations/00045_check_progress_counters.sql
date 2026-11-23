-- +goose Up
-- An active Check carries running counters for its progress view and the
-- Checks tab, so neither counts the Check's Fetches or the Project's Pages: the
-- Fetches planned, the Fetches done, the Fetches that failed, and the Pages its
-- URL discovery added to the pool. They change only in the transactions that
-- hold the Check row lock (a planning batch, a lane step). An ended Check's row
-- reads its stored Report counts instead.
ALTER TABLE checks
    ADD COLUMN progress_known bigint NOT NULL DEFAULT 0,
    ADD COLUMN progress_fetched bigint NOT NULL DEFAULT 0,
    ADD COLUMN progress_failed bigint NOT NULL DEFAULT 0,
    ADD COLUMN progress_discovered bigint NOT NULL DEFAULT 0;

-- Checks active now start with counters that match their rows. A worker still
-- running the old code adds no more until it restarts: the numbers lag, and the
-- Check ends from its Fetches as before.
UPDATE checks c SET
    progress_known = (SELECT count(*) FROM fetches f WHERE f.check_id = c.id),
    progress_fetched = (SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.state = 'done'),
    progress_failed = (SELECT count(*) FROM fetches f WHERE f.check_id = c.id AND f.outcome = 'failure'),
    progress_discovered = (SELECT count(*) FROM pages p
                           WHERE p.project_id = c.project_id AND p.origin = 'discovered' AND p.created_at >= c.started_at)
WHERE c.state IN ('queued', 'planning', 'running', 'cancelling');

-- +goose Down
ALTER TABLE checks
    DROP COLUMN progress_known,
    DROP COLUMN progress_fetched,
    DROP COLUMN progress_failed,
    DROP COLUMN progress_discovered;
