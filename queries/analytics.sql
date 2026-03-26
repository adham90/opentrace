-- Analytics: endpoint stats and traffic heatmap.
-- Only STATIC queries here; aggregation and dynamic queries use squirrel.

-- name: PruneEndpointStats :execrows
DELETE FROM endpoint_stats WHERE period_start < sqlc.arg(cutoff);

-- name: PruneTrafficHeatmap :exec
DELETE FROM traffic_heatmap WHERE updated_at < sqlc.arg(cutoff);

-- DYNAMIC: use squirrel for AggregateEndpointStats (iterative period loop with per-endpoint p95 subqueries)
-- DYNAMIC: use squirrel for UpdateTrafficHeatmap (complex strftime aggregation + upsert loop)
-- DYNAMIC: use squirrel for TopEndpoints (dynamic service/since/until/sort/minRequests filters with HAVING)
-- DYNAMIC: use squirrel for TrafficSummary (dynamic service filter with multiple sub-queries for breakdowns)
-- DYNAMIC: use squirrel for TrafficHeatmap (optional service filter)
