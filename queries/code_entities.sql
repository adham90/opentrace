-- Code entities: file/controller/endpoint risk registry.
-- Table: code_entities (migration 000042)
-- NOTE: BatchGetRisk uses dynamic IN(...) clause -- skip here, will use squirrel.

-- name: UpsertCodeEntity :exec
INSERT INTO code_entities (entity_type, entity_name, service, created_at, updated_at)
VALUES (sqlc.arg(entity_type), sqlc.arg(entity_name), sqlc.arg(service), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET updated_at = sqlc.arg(now);

-- name: GetCodeEntityByName :one
SELECT id, entity_type, entity_name, service, risk_score, error_count,
       investigation_count, avg_duration_ms, last_error_at, last_investigation_at,
       metadata_json, created_at, updated_at
FROM code_entities
WHERE entity_type = sqlc.arg(entity_type) AND entity_name = sqlc.arg(entity_name) AND service = sqlc.arg(service);

-- name: ListTopCodeEntitiesByRiskForService :many
SELECT id, entity_type, entity_name, service, risk_score, error_count,
       investigation_count, avg_duration_ms, last_error_at, last_investigation_at,
       metadata_json, created_at, updated_at
FROM code_entities
WHERE service = sqlc.arg(service)
ORDER BY risk_score DESC
LIMIT sqlc.arg(max_results);

-- name: ListTopCodeEntitiesByRisk :many
SELECT id, entity_type, entity_name, service, risk_score, error_count,
       investigation_count, avg_duration_ms, last_error_at, last_investigation_at,
       metadata_json, created_at, updated_at
FROM code_entities
ORDER BY risk_score DESC
LIMIT sqlc.arg(max_results);

-- name: IncrementCodeEntityError :exec
INSERT INTO code_entities (entity_type, entity_name, service, error_count, last_error_at, created_at, updated_at)
VALUES (sqlc.arg(entity_type), sqlc.arg(entity_name), sqlc.arg(service), 1, sqlc.arg(now), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET
  error_count = error_count + 1,
  last_error_at = sqlc.arg(now),
  updated_at = sqlc.arg(now);

-- name: IncrementCodeEntityInvestigation :exec
INSERT INTO code_entities (entity_type, entity_name, service, investigation_count, last_investigation_at, created_at, updated_at)
VALUES (sqlc.arg(entity_type), sqlc.arg(entity_name), sqlc.arg(service), 1, sqlc.arg(now), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET
  investigation_count = investigation_count + 1,
  last_investigation_at = sqlc.arg(now),
  updated_at = sqlc.arg(now);

-- name: BatchRecomputeCodeEntityRisk :exec
UPDATE code_entities SET
  risk_score = (
    0.4 * MIN(error_count / 10.0, 1.0) +
    0.3 * MIN(investigation_count / 5.0, 1.0) +
    0.2 * CASE
      WHEN last_error_at IS NULL THEN 0.0
      WHEN (julianday(sqlc.arg(now)) - julianday(last_error_at)) < 1.0 THEN 1.0
      WHEN (julianday(sqlc.arg(now)) - julianday(last_error_at)) < 7.0 THEN
        1.0 - ((julianday(sqlc.arg(now)) - julianday(last_error_at)) / 7.0)
      ELSE 0.0
    END +
    0.1 * CASE
      WHEN last_investigation_at IS NULL THEN 0.0
      WHEN (julianday(sqlc.arg(now)) - julianday(last_investigation_at)) < 7.0 THEN 1.0
      ELSE 0.0
    END
  ),
  updated_at = sqlc.arg(now)
WHERE error_count > 0 OR investigation_count > 0;

-- name: PruneCodeEntities :execrows
DELETE FROM code_entities
WHERE error_count = 0 AND investigation_count = 0 AND updated_at < sqlc.arg(cutoff);
