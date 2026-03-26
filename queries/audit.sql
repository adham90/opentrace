-- Audit log: tracks user actions for security and compliance.

-- name: InsertAuditLog :exec
INSERT INTO audit_log (user_id, user_email, action, target_type, target_id, details, ip_address)
VALUES (sqlc.arg(user_id), sqlc.arg(user_email), sqlc.arg(action), sqlc.arg(target_type), sqlc.arg(target_id), sqlc.arg(details), sqlc.arg(ip_address));

-- name: ListRecentAuditLogs :many
SELECT id, user_id, user_email, action,
       COALESCE(target_type, '') AS target_type,
       COALESCE(target_id, '') AS target_id,
       COALESCE(details, '') AS details,
       COALESCE(ip_address, '') AS ip_address,
       created_at
FROM audit_log
ORDER BY created_at DESC
LIMIT sqlc.arg(max_results);

-- name: PruneAuditLogs :execrows
DELETE FROM audit_log
WHERE rowid IN (
    SELECT a.rowid FROM audit_log a WHERE a.created_at < sqlc.arg(cutoff) LIMIT 1000
);
