-- Ownership gate: who owns the Project behind a resource. One PK join to
-- projects.owner_id per kind; no row means the resource does not exist.

-- name: ProjectOwner :one
SELECT owner_id FROM projects WHERE id = $1;

-- name: PageOwner :one
SELECT pr.owner_id FROM pages p JOIN projects pr ON pr.id = p.project_id WHERE p.id = $1;

-- name: CheckOwner :one
SELECT pr.owner_id FROM checks c JOIN projects pr ON pr.id = c.project_id WHERE c.id = $1;

-- name: SnapshotOwner :one
SELECT pr.owner_id
FROM snapshots s
JOIN pages p ON p.id = s.page_id
JOIN projects pr ON pr.id = p.project_id
WHERE s.id = $1;
