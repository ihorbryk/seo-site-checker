-- +goose Up
-- Every Check gets a Check number: its position among its Project's Checks,
-- from 1 in creation order. projects.last_check_number is the counter
-- CreateCheck raises under the Project row's lock (ADR 0001).
ALTER TABLE projects ADD COLUMN last_check_number bigint NOT NULL DEFAULT 0;
ALTER TABLE checks ADD COLUMN number bigint;

UPDATE checks c SET number = n.number
FROM (
    SELECT id, row_number() OVER (PARTITION BY project_id ORDER BY created_at, id) AS number
    FROM checks
) n
WHERE n.id = c.id;

UPDATE projects p SET last_check_number = COALESCE(
    (SELECT max(c.number) FROM checks c WHERE c.project_id = p.id), 0);

ALTER TABLE checks
    ALTER COLUMN number SET NOT NULL,
    ADD CONSTRAINT checks_project_id_number_key UNIQUE (project_id, number);

-- +goose Down
ALTER TABLE checks DROP COLUMN number;
ALTER TABLE projects DROP COLUMN last_check_number;
