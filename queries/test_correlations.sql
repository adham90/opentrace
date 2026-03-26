-- Test correlations: uncovered error paths prioritized for test coverage.
-- Table: uncovered_error_paths (migration 000043)
-- NOTE: TopByPriority has two variants (with/without service filter).

-- name: RefreshUncoveredPaths :exec
INSERT OR REPLACE INTO uncovered_error_paths
    (error_fingerprint, service, error_class, source_file, endpoint,
     error_count, user_impact_score, investigation_count, priority_score,
     last_seen_at, updated_at)
SELECT
    eg.fingerprint,
    COALESCE(eg.service, ''),
    COALESCE(eg.exception_class, ''),
    COALESCE(eg.source_file, ''),
    COALESCE(ce.entity_name, ''),
    eg.occurrence_count,
    COALESCE(eg.impact_score, 0.0),
    COALESCE(ce.investigation_count, 0),
    eg.occurrence_count * MAX(COALESCE(eg.impact_score, 1.0), 1.0) * (1 + COALESCE(ce.investigation_count, 0)),
    COALESCE(eg.last_seen_at, sqlc.arg(now)),
    sqlc.arg(now)
FROM error_groups eg
LEFT JOIN code_entities ce ON (
    ce.entity_type = 'endpoint'
    AND ce.service = eg.service
)
WHERE eg.status = 'unresolved'
  AND eg.occurrence_count >= 3;

-- name: ListTopByPriorityForService :many
SELECT id, service, error_fingerprint, error_class, source_file, endpoint,
       error_count, user_impact_score, investigation_count, priority_score,
       last_seen_at, created_at, updated_at
FROM uncovered_error_paths
WHERE service = sqlc.arg(service)
ORDER BY priority_score DESC
LIMIT sqlc.arg(max_results);

-- name: ListTopByPriority :many
SELECT id, service, error_fingerprint, error_class, source_file, endpoint,
       error_count, user_impact_score, investigation_count, priority_score,
       last_seen_at, created_at, updated_at
FROM uncovered_error_paths
ORDER BY priority_score DESC
LIMIT sqlc.arg(max_results);

-- name: GetByFingerprint :one
SELECT id, service, error_fingerprint, error_class, source_file, endpoint,
       error_count, user_impact_score, investigation_count, priority_score,
       last_seen_at, created_at, updated_at
FROM uncovered_error_paths
WHERE error_fingerprint = sqlc.arg(fingerprint);

-- name: PruneTestCorrelations :execrows
DELETE FROM uncovered_error_paths
WHERE rowid IN (
  SELECT u.rowid FROM uncovered_error_paths u WHERE u.updated_at < sqlc.arg(cutoff) LIMIT 1000
);
