-- Data sources: external database/log/monitoring connections managed by OpenTrace.

-- name: CreateDataSource :exec
INSERT INTO data_sources (id, type, name, config, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(type), sqlc.arg(name), sqlc.arg(config), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetDataSourceByID :one
SELECT id, type, name, config, status, status_message, last_tested_at, created_at, updated_at
FROM data_sources
WHERE id = sqlc.arg(id);

-- name: ListAllDataSources :many
SELECT id, type, name, config, status, status_message, last_tested_at, created_at, updated_at
FROM data_sources
ORDER BY created_at DESC;

-- name: ListDataSourcesByType :many
SELECT id, type, name, config, status, status_message, last_tested_at, created_at, updated_at
FROM data_sources
WHERE type = sqlc.arg(type)
ORDER BY created_at DESC;

-- name: DeleteDataSource :execrows
DELETE FROM data_sources
WHERE id = sqlc.arg(id);
