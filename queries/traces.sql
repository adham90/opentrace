-- Traces: distributed trace reassembly status tracking.
-- Table: trace_status (migration 000037)
-- NOTE: UpsertTraceStatus in the Go code uses a transaction with read-modify-write
-- for services list merging. The insert/update below are the individual SQL statements
-- used within that transaction.

-- name: GetTraceStatusByID :one
SELECT trace_id, span_count, root_span_id, services,
       first_seen_at, last_updated_at, duration_ms, status, has_errors
FROM trace_status WHERE trace_id = sqlc.arg(trace_id);

-- name: InsertTraceStatus :exec
INSERT INTO trace_status (trace_id, span_count, root_span_id, services,
                          first_seen_at, last_updated_at, duration_ms, status, has_errors)
VALUES (sqlc.arg(trace_id), sqlc.arg(span_count), sqlc.arg(root_span_id), sqlc.arg(services),
        sqlc.arg(first_seen_at), sqlc.arg(last_updated_at), sqlc.arg(duration_ms),
        sqlc.arg(status), sqlc.arg(has_errors));

-- name: UpdateTraceStatus :exec
UPDATE trace_status
SET span_count = sqlc.arg(span_count),
    root_span_id = sqlc.arg(root_span_id),
    services = sqlc.arg(services),
    last_updated_at = sqlc.arg(last_updated_at),
    duration_ms = sqlc.arg(duration_ms),
    has_errors = sqlc.arg(has_errors)
WHERE trace_id = sqlc.arg(trace_id);

-- name: CountTraceStatuses :one
SELECT COUNT(*) FROM trace_status;

-- name: ListRecentTraces :many
SELECT trace_id, span_count, root_span_id, services,
       first_seen_at, last_updated_at, duration_ms, status, has_errors
FROM trace_status
ORDER BY last_updated_at DESC
LIMIT sqlc.arg(max_results) OFFSET sqlc.arg(results_offset);

-- name: MarkStaleTraces :execrows
UPDATE trace_status SET status = 'timeout'
WHERE status = 'partial' AND last_updated_at < sqlc.arg(cutoff);
