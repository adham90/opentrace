-- Sessions: authentication session management.
-- Queries for the sessions table.

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, token, expires_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(token), sqlc.arg(expires_at), sqlc.arg(created_at))
RETURNING *;

-- name: GetSessionByToken :one
SELECT id, user_id, token, expires_at, created_at
FROM sessions WHERE token = sqlc.arg(token) AND expires_at > sqlc.arg(now);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = sqlc.arg(id);

-- name: DeleteSessionsByToken :exec
DELETE FROM sessions WHERE token = sqlc.arg(token);

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= sqlc.arg(now);

-- name: DeleteAllSessionsForUser :exec
DELETE FROM sessions WHERE user_id = sqlc.arg(user_id);
