-- Error groups: aggregate errors by fingerprint (Sentry-lite).
-- Queries for error_groups and error_group_events tables.

-- name: InsertErrorGroup :exec
INSERT INTO error_groups (fingerprint, service, environment, exception_class,
    message, source_file, source_line, status, first_seen_at, last_seen_at,
    occurrence_count, last_log_id)
VALUES (sqlc.arg(fingerprint), sqlc.arg(service), sqlc.arg(environment), sqlc.arg(exception_class),
    sqlc.arg(message), sqlc.arg(source_file), sqlc.arg(source_line), 'unresolved',
    sqlc.arg(first_seen_at), sqlc.arg(last_seen_at), 1, sqlc.arg(last_log_id));

-- name: GetErrorGroupStatus :one
SELECT status FROM error_groups WHERE fingerprint = sqlc.arg(fingerprint);

-- name: IncrementErrorGroupCount :exec
UPDATE error_groups
SET occurrence_count = occurrence_count + 1,
    last_seen_at = sqlc.arg(last_seen_at),
    last_log_id = sqlc.arg(last_log_id)
WHERE fingerprint = sqlc.arg(fingerprint);

-- name: ReopenErrorGroup :exec
UPDATE error_groups
SET status = 'unresolved',
    reopened_count = reopened_count + 1,
    resolved_at = NULL
WHERE fingerprint = sqlc.arg(fingerprint);

-- name: GetErrorGroup :one
SELECT fingerprint, service, environment, exception_class, message,
       source_file, source_line, status, first_seen_at, last_seen_at,
       occurrence_count, last_log_id, reopened_count, resolved_at, ignored_at,
       unique_users, impact_score, COALESCE(common_context, '{}') AS common_context
FROM error_groups WHERE fingerprint = sqlc.arg(fingerprint);

-- DYNAMIC: use squirrel for ListErrorGroups with optional status/service/environment/sort filters

-- name: CountAllErrorGroups :one
SELECT COUNT(*) FROM error_groups;

-- name: CountErrorGroupsByStatus :one
SELECT COUNT(*) FROM error_groups WHERE status = sqlc.arg(status);

-- name: ResolveErrorGroup :execrows
UPDATE error_groups SET status = 'resolved', resolved_at = sqlc.arg(resolved_at)
WHERE fingerprint = sqlc.arg(fingerprint);

-- name: IgnoreErrorGroup :execrows
UPDATE error_groups SET status = 'ignored', ignored_at = sqlc.arg(ignored_at)
WHERE fingerprint = sqlc.arg(fingerprint);

-- name: InsertErrorGroupEvent :exec
INSERT INTO error_group_events (fingerprint, action, reason, created_at)
VALUES (sqlc.arg(fingerprint), sqlc.arg(action), sqlc.arg(reason), sqlc.arg(created_at));

-- name: ListErrorGroupEvents :many
SELECT id, fingerprint, action, reason, created_at
FROM error_group_events
WHERE fingerprint = sqlc.arg(fingerprint)
ORDER BY created_at DESC
LIMIT sqlc.arg(row_limit);

-- name: PruneErrorGroups :execrows
DELETE FROM error_groups WHERE last_seen_at < sqlc.arg(cutoff);
