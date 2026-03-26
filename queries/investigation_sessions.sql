-- Investigation sessions: tracks MCP investigation lifecycle with full identity chain.
-- Only STATIC queries here; dynamic Update/List/FindRecent/FindSimilar/FindBy* use squirrel.

-- name: CreateInvestigationSession :exec
INSERT INTO investigation_sessions
    (id, user_id, user_email, user_role, client_name, client_version,
     workspace, transport, connection_id, status, started_at, last_activity_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(user_email), sqlc.arg(user_role),
    sqlc.arg(client_name), sqlc.arg(client_version),
    sqlc.arg(workspace), sqlc.arg(transport), sqlc.arg(connection_id), 'open',
    sqlc.arg(started_at), sqlc.arg(last_activity_at));

-- name: GetInvestigationSession :one
SELECT id, user_id, user_email, user_role,
    client_name, client_version, workspace, transport, connection_id,
    intent, intent_detail, primary_service, primary_datasource_id,
    status, summary, root_cause, fix_description,
    created_watcher_ids, triggered_by_alert_id, triggered_by_watcher_id,
    resolved_error_group_ids, investigated_error_fingerprints,
    created_healthcheck_ids, triggered_by_healthcheck_id,
    created_note_ids, auto_note_ids,
    runbooks_executed, explained_queries, killed_queries, trace_ids,
    correlated_deploy, pre_investigation_snapshot, post_investigation_snapshot,
    total_steps, total_errors, tool_sequence, tool_fingerprint, arg_signature,
    started_at, last_activity_at, ended_at, duration_seconds,
    recurrence_group, recurrence_count, previous_session_id, fix_durability_seconds,
    files_modified, files_read, linked_deploy_id
FROM investigation_sessions
WHERE id = sqlc.arg(id);

-- name: GetInvestigationSessionStartedAt :one
SELECT started_at FROM investigation_sessions WHERE id = sqlc.arg(id);

-- name: CloseInvestigationSession :exec
UPDATE investigation_sessions
SET ended_at = sqlc.arg(ended_at), duration_seconds = sqlc.arg(duration_seconds),
    last_activity_at = sqlc.arg(last_activity_at),
    status = CASE WHEN status = 'open' THEN 'unresolved' ELSE status END
WHERE id = sqlc.arg(id);

-- name: GetInvestigationSessionToolSequence :one
SELECT tool_sequence FROM investigation_sessions WHERE id = sqlc.arg(id);

-- name: RecordInvestigationStep :exec
UPDATE investigation_sessions
SET total_steps = total_steps + 1,
    total_errors = total_errors + sqlc.arg(error_increment),
    tool_sequence = sqlc.arg(tool_sequence),
    tool_fingerprint = sqlc.arg(tool_fingerprint),
    last_activity_at = sqlc.arg(last_activity_at)
WHERE id = sqlc.arg(id);

-- name: GetInvestigationSessionStatsTotalSessions :one
SELECT COUNT(*) FROM investigation_sessions;

-- name: GetInvestigationSessionStatsOpenSessions :one
SELECT COUNT(*) FROM investigation_sessions WHERE status = 'open';

-- name: GetInvestigationSessionStatsResolvedSessions :one
SELECT COUNT(*) FROM investigation_sessions WHERE status = 'resolved';

-- name: GetInvestigationSessionStatsAverages :one
SELECT AVG(total_steps) AS avg_steps, AVG(duration_seconds) AS avg_duration
FROM investigation_sessions WHERE status != 'open';

-- name: PruneInvestigationSessions :execrows
DELETE FROM investigation_sessions
WHERE rowid IN (SELECT i.rowid FROM investigation_sessions i WHERE i.ended_at IS NOT NULL AND i.ended_at < sqlc.arg(cutoff) LIMIT 1000);

-- DYNAMIC: use squirrel for Update (30+ optional SET fields)
-- DYNAMIC: use squirrel for FindRecent (user_id + workspace + status + max_age filters)
-- DYNAMIC: use squirrel for List (user_id/status/intent/service/since optional filters)
-- DYNAMIC: use squirrel for FindSimilar (exclude_id + service + intent + resolved + tool_fingerprint prefix)
-- DYNAMIC: use squirrel for FindByCreatedWatcher (JSON LIKE pattern)
-- DYNAMIC: use squirrel for FindByResolvedError (JSON LIKE pattern)
-- DYNAMIC: use squirrel for FindByCreatedHealthcheck (JSON LIKE pattern)
