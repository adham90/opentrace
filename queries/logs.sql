-- Logs: core log ingestion and retrieval.
-- Only STATIC queries here; dynamic search/filter queries use squirrel.

-- name: InsertLog :exec
INSERT INTO logs (timestamp, level, service, environment, commit_hash,
    trace_id, span_id, parent_span_id, request_id,
    user_id, session_id,
    message, event_type, exception_class, error_fingerprint,
    source_file, source_line, metadata)
VALUES (sqlc.arg(timestamp), sqlc.arg(level), sqlc.arg(service), sqlc.arg(environment), sqlc.arg(commit_hash),
    sqlc.arg(trace_id), sqlc.arg(span_id), sqlc.arg(parent_span_id), sqlc.arg(request_id),
    sqlc.arg(user_id), sqlc.arg(session_id),
    sqlc.arg(message), sqlc.arg(event_type), sqlc.arg(exception_class), sqlc.arg(error_fingerprint),
    sqlc.arg(source_file), sqlc.arg(source_line), sqlc.arg(metadata));

-- name: GetLogByID :one
SELECT id, timestamp, level, service, environment, commit_hash,
    trace_id, span_id, parent_span_id, request_id,
    user_id, session_id,
    message, event_type, exception_class, error_fingerprint,
    source_file, source_line, metadata
FROM logs WHERE id = sqlc.arg(id);

-- name: RecordBatch :exec
INSERT OR IGNORE INTO ingest_batches (batch_id, log_count) VALUES (sqlc.arg(batch_id), sqlc.arg(log_count));

-- name: GetBatch :one
SELECT batch_id, log_count, received_at FROM ingest_batches WHERE batch_id = sqlc.arg(batch_id);

-- name: PruneBatches :execrows
DELETE FROM ingest_batches WHERE received_at < sqlc.arg(cutoff);

-- name: PruneLogs :execrows
DELETE FROM logs WHERE rowid IN (SELECT l.rowid FROM logs l WHERE l.timestamp < sqlc.arg(cutoff) LIMIT 1000);

-- DYNAMIC: use squirrel for Search (FTS, multi-value filters, metadata JSON, exclude, sort)
-- DYNAMIC: use squirrel for CountByLevel (optional service/level filters)
-- DYNAMIC: use squirrel for CountByService (optional service/level filters)
-- DYNAMIC: use squirrel for Histogram (iterative bucket queries with optional filters)
-- DYNAMIC: use squirrel for DistinctValues (dynamic column selection)
-- DYNAMIC: use squirrel for MetadataKeys (json_each with optional service filter)
-- DYNAMIC: use squirrel for SearchRequestSummaries (complex join with optional filters)
