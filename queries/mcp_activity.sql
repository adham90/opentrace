-- MCP activity: tracks tool calls, connections, and disconnections from MCP clients.

-- name: InsertMCPActivity :exec
INSERT INTO mcp_activity (
    session_id, user_id, tool_name, arguments, result_preview,
    is_error, duration_ms, event_type,
    investigation_session_id, step_index,
    was_suggested, suggestion_rank, followed_by
)
VALUES (
    sqlc.arg(session_id), sqlc.arg(user_id), sqlc.arg(tool_name),
    sqlc.arg(arguments), sqlc.arg(result_preview),
    sqlc.arg(is_error), sqlc.arg(duration_ms), sqlc.arg(event_type),
    sqlc.arg(investigation_session_id), sqlc.arg(step_index),
    sqlc.arg(was_suggested), sqlc.arg(suggestion_rank), sqlc.arg(followed_by)
);

-- name: GetMCPActiveSessionCount :one
SELECT COUNT(DISTINCT session_id) FROM mcp_activity
WHERE created_at > sqlc.arg(since);

-- name: GetMCPCallCountSince :one
SELECT COUNT(*) FROM mcp_activity
WHERE event_type = 'tool_call' AND created_at > sqlc.arg(since);

-- name: GetMCPErrorCountSince :one
SELECT COUNT(*) FROM mcp_activity
WHERE event_type = 'tool_call' AND is_error = 1 AND created_at > sqlc.arg(since);

-- name: GetMCPLastActivityTime :one
SELECT MAX(created_at) FROM mcp_activity;

-- name: ListRecentMCPActivity :many
SELECT id, session_id, COALESCE(user_id, '') AS user_id, tool_name,
       COALESCE(arguments, '') AS arguments,
       COALESCE(result_preview, '') AS result_preview,
       is_error, duration_ms, event_type, created_at
FROM mcp_activity
ORDER BY created_at DESC
LIMIT sqlc.arg(max_results);

-- name: ListMCPActivityByInvestigationSession :many
SELECT id, session_id, COALESCE(user_id, '') AS user_id, tool_name,
       COALESCE(arguments, '') AS arguments,
       COALESCE(result_preview, '') AS result_preview,
       is_error, duration_ms, event_type, created_at
FROM mcp_activity
WHERE investigation_session_id = sqlc.arg(investigation_session_id)
ORDER BY step_index ASC, created_at ASC;

-- name: PruneMCPActivity :execrows
DELETE FROM mcp_activity
WHERE rowid IN (
    SELECT m.rowid FROM mcp_activity m WHERE m.created_at < sqlc.arg(cutoff) LIMIT 1000
);

-- name: SetSuggestionTracking :exec
UPDATE mcp_activity
SET was_suggested = sqlc.arg(was_suggested), suggestion_rank = sqlc.arg(suggestion_rank)
WHERE investigation_session_id = sqlc.arg(investigation_session_id) AND step_index = sqlc.arg(step_index);

-- name: UpdateFollowedBy :exec
UPDATE mcp_activity
SET followed_by = sqlc.arg(followed_by)
WHERE investigation_session_id = sqlc.arg(investigation_session_id) AND step_index = sqlc.arg(step_index);
