-- Query memory: stores explain_query findings across investigation sessions.
-- Table: query_memory (migration 000041)

-- name: GetQueryMemory :one
SELECT fingerprint, last_investigation_session_id, investigation_count,
       last_root_cause, last_fix, avg_duration_before_ms, avg_duration_after_ms,
       first_seen_at, last_seen_at
FROM query_memory WHERE fingerprint = sqlc.arg(fingerprint);

-- name: UpsertQueryMemory :exec
INSERT INTO query_memory (fingerprint, last_investigation_session_id, investigation_count,
                          last_root_cause, last_fix, avg_duration_before_ms,
                          first_seen_at, last_seen_at)
VALUES (sqlc.arg(fingerprint), sqlc.arg(session_id), 1,
        sqlc.arg(root_cause), sqlc.arg(fix), sqlc.arg(duration_ms),
        sqlc.arg(now), sqlc.arg(now))
ON CONFLICT(fingerprint) DO UPDATE SET
    investigation_count = investigation_count + 1,
    last_investigation_session_id = sqlc.arg(session_id),
    last_root_cause = CASE WHEN sqlc.arg(root_cause) != '' THEN sqlc.arg(root_cause) ELSE last_root_cause END,
    last_fix = CASE WHEN sqlc.arg(fix) != '' THEN sqlc.arg(fix) ELSE last_fix END,
    last_seen_at = sqlc.arg(now);

-- name: PruneQueryMemory :execrows
DELETE FROM query_memory WHERE last_seen_at < sqlc.arg(cutoff);
