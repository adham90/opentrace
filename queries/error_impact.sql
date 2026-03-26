-- Error impact: cohort-based error impact tracking.
-- Queries for error_impacts table and impact-related error_groups updates.

-- name: TrackErrorImpact :exec
INSERT INTO error_impacts (error_fingerprint, user_id, service, first_seen_at, last_seen_at, occurrence_count, last_context, last_log_id)
VALUES (sqlc.arg(error_fingerprint), sqlc.arg(user_id), sqlc.arg(service), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at), 1, sqlc.arg(last_context), sqlc.arg(last_log_id))
ON CONFLICT(error_fingerprint, user_id) DO UPDATE SET
    last_seen_at = excluded.last_seen_at,
    occurrence_count = error_impacts.occurrence_count + 1,
    last_context = excluded.last_context,
    last_log_id = excluded.last_log_id;

-- name: GetErrorImpact :one
SELECT COUNT(DISTINCT user_id) AS unique_users,
       SUM(occurrence_count) AS total_occurrences
FROM error_impacts
WHERE error_fingerprint = sqlc.arg(fingerprint);

-- name: GetErrorImpactScore :one
SELECT COALESCE(impact_score, 0) AS impact_score
FROM error_groups WHERE fingerprint = sqlc.arg(fingerprint);

-- name: ListAffectedUsers :many
SELECT user_id, occurrence_count, first_seen_at, last_seen_at, last_context
FROM error_impacts
WHERE error_fingerprint = sqlc.arg(fingerprint)
ORDER BY occurrence_count DESC
LIMIT sqlc.arg(row_limit);

-- name: AggregateImpactData :many
SELECT error_fingerprint,
       COUNT(DISTINCT user_id) AS unique_users,
       SUM(occurrence_count) AS total_occurrences,
       MAX(last_seen_at) AS last_seen
FROM error_impacts
GROUP BY error_fingerprint;

-- name: UpdateErrorGroupImpact :exec
UPDATE error_groups
SET unique_users = sqlc.arg(unique_users), impact_score = sqlc.arg(impact_score), common_context = sqlc.arg(common_context)
WHERE fingerprint = sqlc.arg(fingerprint);

-- DYNAMIC: use squirrel for GetUserErrors (joins error_impacts + error_groups with user_id + since filters)
-- DYNAMIC: use squirrel for TopByImpact (dynamic status/service/since/sort filters)
-- DYNAMIC: use squirrel for FindCommonTraits (application-level aggregation of JSON contexts)

-- name: PruneErrorImpacts :execrows
DELETE FROM error_impacts WHERE last_seen_at < sqlc.arg(cutoff);
