-- Metrics: time-series data points collected from server agents.

-- name: InsertMetric :exec
INSERT INTO metrics (server_id, timestamp, metric_name, metric_value, unit, labels, created_at)
VALUES (sqlc.arg(server_id), sqlc.arg(timestamp), sqlc.arg(metric_name), sqlc.arg(metric_value), sqlc.arg(unit), sqlc.arg(labels), sqlc.arg(created_at));

-- name: GetLatestMetricsByServer :many
SELECT m.id, m.server_id, m.timestamp, m.metric_name, m.metric_value, m.unit, m.labels
FROM metrics m
INNER JOIN (
    SELECT m2.metric_name, MAX(m2.timestamp) AS max_ts
    FROM metrics m2
    WHERE m2.server_id = sqlc.arg(server_id)
    GROUP BY m2.metric_name
) latest ON m.metric_name = latest.metric_name AND m.timestamp = latest.max_ts
WHERE m.server_id = sqlc.arg(server_id)
ORDER BY m.metric_name;

-- name: PruneMetrics :execrows
DELETE FROM metrics
WHERE rowid IN (
    SELECT m3.rowid FROM metrics m3 WHERE m3.created_at < sqlc.arg(cutoff) LIMIT 1000
);
