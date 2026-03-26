-- Trends: metric buckets, deploy markers, and time-series data.
-- Only STATIC queries here; aggregation and dynamic queries use squirrel.

-- name: PruneTrendBuckets :execrows
DELETE FROM metric_buckets WHERE bucket_start < sqlc.arg(cutoff);

-- name: PruneDeployMarkers :execrows
DELETE FROM deploy_markers WHERE first_seen_at < sqlc.arg(cutoff);

-- name: ListDeployMarkersSince :many
SELECT id, service, environment, commit_hash, first_seen_at, request_count
FROM deploy_markers
WHERE first_seen_at >= sqlc.arg(since)
ORDER BY first_seen_at ASC;

-- name: ListDeployMarkersByServiceSince :many
SELECT id, service, environment, commit_hash, first_seen_at, request_count
FROM deploy_markers
WHERE first_seen_at >= sqlc.arg(since) AND service = sqlc.arg(service)
ORDER BY first_seen_at ASC;

-- DYNAMIC: use squirrel for AggregateBuckets (iterative time-window loop with multi-query aggregation + upsert)
-- DYNAMIC: use squirrel for QueryTrends (dynamic interval/since/until/service/endpoint/environment filters)
