-- Servers: registered agent hosts reporting metrics to OpenTrace.

-- name: RegisterServer :exec
INSERT INTO servers (id, hostname, display_name, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(hostname), sqlc.arg(display_name), sqlc.arg(ip_address), sqlc.arg(os), sqlc.arg(arch), sqlc.arg(agent_version), sqlc.arg(labels), sqlc.arg(status), sqlc.arg(last_seen_at), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetServerByID :one
SELECT id, hostname, display_name, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at
FROM servers
WHERE id = sqlc.arg(id);

-- name: ListServers :many
SELECT id, hostname, display_name, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at
FROM servers
ORDER BY hostname ASC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: UpdateServerDisplayName :exec
UPDATE servers
SET display_name = sqlc.arg(display_name), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: UpdateServerHeartbeat :execrows
UPDATE servers
SET status = 'online', last_seen_at = sqlc.arg(last_seen_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteServer :execrows
DELETE FROM servers
WHERE id = sqlc.arg(id);

-- name: MarkStaleServersOffline :execrows
UPDATE servers
SET status = 'offline', updated_at = sqlc.arg(updated_at)
WHERE status = 'online' AND last_seen_at < sqlc.arg(cutoff);
