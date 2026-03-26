-- Users: authentication and authorization.
-- Queries for the users table.

-- name: CreateUser :exec
INSERT INTO users (id, email, password_hash, display_name, role, mcp_token, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.arg(password_hash), sqlc.arg(display_name), sqlc.arg(role), sqlc.arg(mcp_token), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetUserByID :one
SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
FROM users WHERE id = sqlc.arg(id);

-- name: GetUserByEmail :one
SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
FROM users WHERE email = sqlc.arg(email);

-- name: GetUserByMCPToken :one
SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
FROM users WHERE mcp_token = sqlc.arg(mcp_token) AND mcp_enabled = 1 AND is_active = 1;

-- name: ListUsers :many
SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
FROM users ORDER BY created_at ASC LIMIT 1000;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CountActiveAdminsExcluding :one
SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = 1 AND id != sqlc.arg(exclude_id);

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = sqlc.arg(id);

-- name: UpdatePassword :execrows
UPDATE users SET password_hash = sqlc.arg(password_hash), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: UpdateMCPToken :execrows
UPDATE users SET mcp_token = sqlc.arg(mcp_token), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- DYNAMIC: use squirrel for Update (optional display_name/role/mcp_enabled/is_active fields)
