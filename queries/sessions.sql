-- name: GetSessionByToken :one
SELECT * FROM sessions WHERE token = ? AND expires_at > datetime('now');

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, token, expires_at, created_at)
VALUES (?, ?, ?, ?, datetime('now'))
RETURNING *;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteSessionsByToken :exec
DELETE FROM sessions WHERE token = ?;

-- name: DeleteExpiredSessions :execresult
DELETE FROM sessions WHERE expires_at <= datetime('now');

-- name: DeleteAllSessionsForUser :exec
DELETE FROM sessions WHERE user_id = ?;
