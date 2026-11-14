-- name: CreateUser :one
INSERT INTO users (email, password_hash)
VALUES (sqlc.arg(email), sqlc.arg(password_hash))
RETURNING *;

-- name: GetUserByEmailForUpdate :one
SELECT * FROM users WHERE email = $1 FOR UPDATE;

-- name: SetUserPasswordHash :exec
UPDATE users SET password_hash = sqlc.arg(password_hash) WHERE id = sqlc.arg(id);

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(now), sqlc.arg(now), sqlc.arg(expires_at));

-- name: DeleteSessionsByUser :execrows
DELETE FROM sessions WHERE user_id = $1;

-- name: CountSessionsByUser :one
SELECT count(*) FROM sessions WHERE user_id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetSessionUser :one
-- The User behind a session token hash, with when the session was last bumped.
-- Absent once past expires_at (30 days from creation) or last seen before idle_cutoff.
SELECT u.id, u.email, u.password_hash, u.created_at, s.last_seen_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg(token_hash)
  AND s.expires_at > sqlc.arg(now)::timestamptz
  AND s.last_seen_at > sqlc.arg(idle_cutoff)::timestamptz;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = sqlc.arg(now)
WHERE token_hash = sqlc.arg(token_hash) AND last_seen_at < sqlc.arg(stale_before)::timestamptz;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;
