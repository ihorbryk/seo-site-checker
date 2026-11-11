-- name: ListNoiseRules :many
SELECT * FROM noise_rules WHERE project_id = $1 ORDER BY position;

-- name: DeleteNoiseRules :exec
DELETE FROM noise_rules WHERE project_id = $1;

-- name: InsertNoiseRule :exec
INSERT INTO noise_rules (project_id, kind, value, position)
VALUES (sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(value), sqlc.arg(position));
