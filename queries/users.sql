-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByMCPToken :one
SELECT * FROM users WHERE mcp_token = ? AND mcp_enabled = 1 AND is_active = 1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at ASC;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: UpdatePassword :exec
UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?;

-- name: UpdateMCPToken :exec
UPDATE users SET mcp_token = ?, updated_at = datetime('now') WHERE id = ?;
