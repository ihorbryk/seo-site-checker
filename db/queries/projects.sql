-- name: CreateProject :one
INSERT INTO projects (owner_id, domain, schedule)
VALUES (sqlc.arg(owner_id), sqlc.arg(domain), sqlc.arg(schedule))
RETURNING *;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY domain;

-- name: GetProjectByDomain :one
SELECT * FROM projects WHERE owner_id = sqlc.arg(owner_id) AND domain = sqlc.arg(domain);

-- name: CreatePage :one
INSERT INTO pages (project_id, url, origin)
VALUES (sqlc.arg(project_id), sqlc.arg(url), sqlc.arg(origin))
RETURNING *;

-- name: ListPagesByProject :many
SELECT * FROM pages WHERE project_id = $1 ORDER BY url;

-- name: UpdateProjectRequestSettings :exec
UPDATE projects SET
    schedule = sqlc.arg(schedule),
    user_agent = sqlc.arg(user_agent),
    custom_headers = sqlc.arg(custom_headers),
    timeout_s = sqlc.arg(timeout_s),
    max_parallel = sqlc.arg(max_parallel),
    respect_robots = sqlc.arg(respect_robots),
    exclude_patterns = sqlc.arg(exclude_patterns)::text[]
WHERE id = sqlc.arg(id);
