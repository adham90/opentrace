-- Runbook effectiveness: tracks resolution rates per playbook.
-- Table: runbook_effectiveness (migration 000041)

-- name: RecordRunbookExecution :exec
INSERT INTO runbook_effectiveness (runbook_name, total_executions, last_executed_at)
VALUES (sqlc.arg(runbook_name), 1, sqlc.arg(now))
ON CONFLICT(runbook_name) DO UPDATE SET
    total_executions = total_executions + 1,
    last_executed_at = sqlc.arg(now);

-- name: UpdateRunbookOutcomeResolved :exec
UPDATE runbook_effectiveness SET
    resolved_sessions = resolved_sessions + 1,
    avg_steps_after = (avg_steps_after * resolved_sessions + sqlc.arg(steps_after)) / (resolved_sessions + 1),
    avg_session_duration_seconds = (avg_session_duration_seconds * resolved_sessions + sqlc.arg(duration_sec)) / (resolved_sessions + 1)
WHERE runbook_name = sqlc.arg(runbook_name);

-- name: UpdateRunbookOutcomeAbandoned :exec
UPDATE runbook_effectiveness SET
    abandoned_sessions = abandoned_sessions + 1,
    avg_steps_after = (avg_steps_after * abandoned_sessions + sqlc.arg(steps_after)) / (abandoned_sessions + 1),
    avg_session_duration_seconds = (avg_session_duration_seconds * abandoned_sessions + sqlc.arg(duration_sec)) / (abandoned_sessions + 1)
WHERE runbook_name = sqlc.arg(runbook_name);

-- name: GetMostEffectiveRunbook :one
SELECT runbook_name, total_executions, resolved_sessions, abandoned_sessions,
       avg_steps_after, avg_session_duration_seconds, last_executed_at
FROM runbook_effectiveness
WHERE total_executions >= 3 AND resolved_sessions > abandoned_sessions
ORDER BY CAST(resolved_sessions AS REAL) / total_executions DESC
LIMIT 1;

-- name: ListRunbookEffectiveness :many
SELECT runbook_name, total_executions, resolved_sessions, abandoned_sessions,
       avg_steps_after, avg_session_duration_seconds, last_executed_at
FROM runbook_effectiveness
ORDER BY total_executions DESC
LIMIT 100;
