-- Health checks: HTTP endpoint monitoring (UptimeRobot-lite).
-- Queries for healthchecks and healthcheck_results tables.

-- name: CreateHealthcheck :exec
INSERT INTO healthchecks (id, name, url, method, interval_secs, timeout_secs, expected_status, expected_body, retries, enabled, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(url), sqlc.arg(method), sqlc.arg(interval_secs), sqlc.arg(timeout_secs), sqlc.arg(expected_status), sqlc.arg(expected_body), sqlc.arg(retries), 1, sqlc.arg(created_at));

-- name: GetHealthcheck :one
SELECT id, name, url, method, interval_secs, timeout_secs, expected_status, expected_body, retries, enabled, created_at
FROM healthchecks WHERE id = sqlc.arg(id);

-- name: ListHealthchecks :many
SELECT id, name, url, method, interval_secs, timeout_secs, expected_status, expected_body, retries, enabled, created_at
FROM healthchecks ORDER BY created_at DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: DeleteHealthcheck :execrows
DELETE FROM healthchecks WHERE id = sqlc.arg(id);

-- name: SetHealthcheckEnabled :execrows
UPDATE healthchecks SET enabled = sqlc.arg(enabled) WHERE id = sqlc.arg(id);

-- name: InsertHealthcheckResult :exec
INSERT INTO healthcheck_results (healthcheck_id, status, status_code, response_ms, error, checked_at)
VALUES (sqlc.arg(healthcheck_id), sqlc.arg(status), sqlc.arg(status_code), sqlc.arg(response_ms), sqlc.arg(error), sqlc.arg(checked_at));

-- name: ListLatestHealthcheckResults :many
SELECT id, healthcheck_id, status, status_code, response_ms, error, checked_at
FROM healthcheck_results
WHERE healthcheck_id = sqlc.arg(healthcheck_id)
ORDER BY checked_at DESC
LIMIT sqlc.arg(row_limit);

-- name: PruneHealthcheckResults :execrows
DELETE FROM healthcheck_results WHERE checked_at < sqlc.arg(cutoff);

-- name: UptimeSummaries :many
SELECT
    h.id,
    h.name,
    h.url,
    COALESCE((SELECT status FROM healthcheck_results WHERE healthcheck_id = h.id ORDER BY checked_at DESC LIMIT 1), 'unknown') AS current_status,
    COUNT(r.id) AS total_checks,
    SUM(CASE WHEN r.status = 'down' THEN 1 ELSE 0 END) AS down_checks,
    COALESCE(AVG(r.response_ms), 0) AS avg_response_ms
FROM healthchecks h
LEFT JOIN healthcheck_results r ON r.healthcheck_id = h.id AND r.checked_at >= sqlc.arg(since)
GROUP BY h.id
ORDER BY h.name;
