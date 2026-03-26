-- Watches: agent-first metric + operator + threshold monitoring.
-- Queries for watches, watch_runs, and watch_alerts tables.

-- name: CreateWatch :exec
INSERT INTO watches (id, metric, operator, threshold, service, endpoint, environment, commit_hash,
    duration, urgency, check_interval, baseline_window, min_consecutive, status,
    consecutive_breaches, expires_at, created_by, session_id, next_check_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(metric), sqlc.arg(operator), sqlc.arg(threshold),
    sqlc.arg(service), sqlc.arg(endpoint), sqlc.arg(environment), sqlc.arg(commit_hash),
    sqlc.arg(duration), sqlc.arg(urgency), sqlc.arg(check_interval), sqlc.arg(baseline_window),
    sqlc.arg(min_consecutive), sqlc.arg(status), 0,
    sqlc.arg(expires_at), sqlc.arg(created_by), sqlc.arg(session_id), sqlc.arg(next_check_at),
    sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetWatch :one
SELECT id, metric, operator, threshold, service, endpoint, environment, commit_hash,
    duration, urgency, check_interval, baseline_window, min_consecutive, status,
    baseline_json, consecutive_breaches, current_value, expires_at, created_by,
    session_id, last_checked_at, next_check_at, created_at, updated_at
FROM watches WHERE id = sqlc.arg(id);

-- name: DeleteWatch :execrows
DELETE FROM watches WHERE id = sqlc.arg(id);

-- name: UpdateWatchStatus :execrows
UPDATE watches SET status = sqlc.arg(status), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: UpdateWatchAfterCheck :execrows
UPDATE watches SET current_value = sqlc.arg(current_value), consecutive_breaches = sqlc.arg(consecutive_breaches),
    last_checked_at = sqlc.arg(last_checked_at), next_check_at = sqlc.arg(next_check_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: UpdateWatchBaseline :execrows
UPDATE watches SET baseline_json = sqlc.arg(baseline_json), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: GetDueWatches :many
SELECT id, metric, operator, threshold, service, endpoint, environment, commit_hash,
    duration, urgency, check_interval, baseline_window, min_consecutive, status,
    baseline_json, consecutive_breaches, current_value, expires_at, created_by,
    session_id, last_checked_at, next_check_at, created_at, updated_at
FROM watches
WHERE status = 'active' AND next_check_at IS NOT NULL AND next_check_at <= sqlc.arg(now)
ORDER BY next_check_at ASC;

-- name: ExpireWatches :execrows
UPDATE watches SET status = 'expired', updated_at = sqlc.arg(now)
WHERE expires_at IS NOT NULL AND expires_at <= sqlc.arg(now) AND status = 'active';

-- DYNAMIC: use squirrel for ListWatches with optional status/service/session_id filters

-- ---- Watch Runs ----

-- name: CreateWatchRun :exec
INSERT INTO watch_runs (id, watch_id, status, started_at) VALUES (sqlc.arg(id), sqlc.arg(watch_id), 'running', sqlc.arg(started_at));

-- name: CompleteWatchRun :execrows
UPDATE watch_runs SET status = 'completed', metric_value = sqlc.arg(metric_value), breached = sqlc.arg(breached),
    summary = sqlc.arg(summary), finished_at = sqlc.arg(finished_at) WHERE id = sqlc.arg(id);

-- name: FailWatchRun :execrows
UPDATE watch_runs SET status = 'failed', error_message = sqlc.arg(error_message), finished_at = sqlc.arg(finished_at) WHERE id = sqlc.arg(id);

-- name: ListWatchRuns :many
SELECT id, watch_id, status, metric_value, breached, summary, error_message, started_at, finished_at
FROM watch_runs WHERE watch_id = sqlc.arg(watch_id) ORDER BY started_at DESC LIMIT sqlc.arg(row_limit);

-- ---- Watch Alerts ----

-- name: CreateWatchAlert :exec
INSERT INTO watch_alerts (id, watch_id, run_id, urgency, summary, trigger_metric, trigger_value, threshold_value, evidence_json, status, created_at)
VALUES (sqlc.arg(id), sqlc.arg(watch_id), sqlc.arg(run_id), sqlc.arg(urgency), sqlc.arg(summary),
    sqlc.arg(trigger_metric), sqlc.arg(trigger_value), sqlc.arg(threshold_value), sqlc.arg(evidence_json), 'pending', sqlc.arg(created_at));

-- name: GetWatchAlert :one
SELECT id, watch_id, run_id, urgency, summary, trigger_metric, trigger_value,
    threshold_value, evidence_json, status, dismiss_reason, created_at
FROM watch_alerts WHERE id = sqlc.arg(id);

-- name: DismissWatchAlert :execrows
UPDATE watch_alerts SET status = 'dismissed', dismiss_reason = sqlc.arg(dismiss_reason) WHERE id = sqlc.arg(id);

-- name: AcknowledgeWatchAlert :execrows
UPDATE watch_alerts SET status = 'acknowledged' WHERE id = sqlc.arg(id);

-- name: CountPendingAlerts :one
SELECT COUNT(*) FROM watch_alerts WHERE status = 'pending';

-- DYNAMIC: use squirrel for ListAlerts with optional watch_id/status filters
