-- name: CreateProject :one
INSERT INTO projects (domain, schedule)
VALUES (sqlc.arg(domain), sqlc.arg(schedule))
RETURNING *;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY domain;

-- name: GetProjectByDomain :one
SELECT * FROM projects WHERE domain = $1;

-- name: CreatePage :one
INSERT INTO pages (project_id, url, origin)
VALUES (sqlc.arg(project_id), sqlc.arg(url), sqlc.arg(origin))
RETURNING *;

-- name: ListPagesByProject :many
SELECT * FROM pages WHERE project_id = $1 ORDER BY url;

-- name: CountActivePagesByProject :many
SELECT project_id, count(*) AS active_pages
FROM pages
WHERE state = 'active'
GROUP BY project_id;
