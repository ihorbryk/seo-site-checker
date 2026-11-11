-- name: CreateProject :one
INSERT INTO projects (domain, schedule)
VALUES (sqlc.arg(domain), sqlc.arg(schedule))
RETURNING *;

-- name: ListProjectsWithActivePages :many
SELECT
    p.*,
    (SELECT count(*) FROM pages g WHERE g.project_id = p.id AND g.state = 'active')::bigint AS active_pages
FROM projects p
ORDER BY p.domain;

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
