-- +goose Up
-- A Check plans its Fetches in batches before its lanes start (ADR 0006). It is
-- `planning` meanwhile, and plan_cursor is the id of the last Page it planned:
-- the next batch starts after it.
ALTER TABLE checks DROP CONSTRAINT checks_state_check;
ALTER TABLE checks ADD CONSTRAINT checks_state_check
    CHECK (state IN ('queued', 'planning', 'running', 'cancelling', 'finished', 'cancelled'));
ALTER TABLE checks ADD COLUMN plan_cursor bigint NOT NULL DEFAULT 0;

DROP INDEX checks_one_active_idx;
CREATE UNIQUE INDEX checks_one_active_idx ON checks (project_id)
    WHERE state IN ('queued', 'planning', 'running', 'cancelling');

-- +goose Down
-- The old StartCheck plans a Check in one go: hand it back the planning
-- Checks as queued, without the Fetches planned so far. Their StartCheck job
-- is still queued, and the old worker ignores its after_page_id.
DELETE FROM fetches WHERE check_id IN (SELECT id FROM checks WHERE state = 'planning');
UPDATE checks SET state = 'queued', started_at = NULL WHERE state = 'planning';

DROP INDEX checks_one_active_idx;
CREATE UNIQUE INDEX checks_one_active_idx ON checks (project_id)
    WHERE state IN ('queued', 'running', 'cancelling');

ALTER TABLE checks DROP COLUMN plan_cursor;
ALTER TABLE checks DROP CONSTRAINT checks_state_check;
ALTER TABLE checks ADD CONSTRAINT checks_state_check
    CHECK (state IN ('queued', 'running', 'cancelling', 'finished', 'cancelled'));
