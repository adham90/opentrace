-- Deploys: deploy tracking and impact measurement.
-- Table: deploys (migration 000042)

-- name: CreateDeploy :one
INSERT INTO deploys (service, environment, commit_hash, branch, author,
                     files_changed_json, deploy_source, deployed_at, created_at)
VALUES (sqlc.arg(service), sqlc.arg(environment), sqlc.arg(commit_hash), sqlc.arg(branch), sqlc.arg(author),
        sqlc.arg(files_changed_json), sqlc.arg(deploy_source), sqlc.arg(deployed_at), sqlc.arg(created_at))
RETURNING *;

-- name: GetDeployByID :one
SELECT id, service, environment, commit_hash, branch, author,
       files_changed_json, deploy_source,
       pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
       impact_measured_at, linked_investigation_ids_json, status,
       deployed_at, created_at
FROM deploys WHERE id = sqlc.arg(id);

-- name: GetDeployByCommit :one
SELECT id, service, environment, commit_hash, branch, author,
       files_changed_json, deploy_source,
       pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
       impact_measured_at, linked_investigation_ids_json, status,
       deployed_at, created_at
FROM deploys WHERE commit_hash = sqlc.arg(commit_hash)
ORDER BY deployed_at DESC LIMIT 1;

-- name: ListRecentDeploysByService :many
SELECT id, service, environment, commit_hash, branch, author,
       files_changed_json, deploy_source,
       pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
       impact_measured_at, linked_investigation_ids_json, status,
       deployed_at, created_at
FROM deploys WHERE service = sqlc.arg(service)
ORDER BY deployed_at DESC LIMIT sqlc.arg(max_results);

-- name: ListRecentDeploys :many
SELECT id, service, environment, commit_hash, branch, author,
       files_changed_json, deploy_source,
       pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
       impact_measured_at, linked_investigation_ids_json, status,
       deployed_at, created_at
FROM deploys
ORDER BY deployed_at DESC LIMIT sqlc.arg(max_results);

-- name: MeasureDeployImpact :exec
UPDATE deploys SET
  pre_error_rate = sqlc.arg(pre_error_rate),
  post_error_rate = sqlc.arg(post_error_rate),
  pre_avg_duration_ms = sqlc.arg(pre_avg_duration_ms),
  post_avg_duration_ms = sqlc.arg(post_avg_duration_ms),
  impact_measured_at = sqlc.arg(impact_measured_at),
  status = sqlc.arg(status)
WHERE id = sqlc.arg(id);

-- name: GetDeployLinkedInvestigations :one
SELECT linked_investigation_ids_json FROM deploys WHERE id = sqlc.arg(id);

-- name: UpdateDeployLinkedInvestigations :exec
UPDATE deploys SET linked_investigation_ids_json = sqlc.arg(linked_investigation_ids_json)
WHERE id = sqlc.arg(id);

-- name: ListPendingMeasurement :many
SELECT id, service, environment, commit_hash, branch, author,
       files_changed_json, deploy_source,
       pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
       impact_measured_at, linked_investigation_ids_json, status,
       deployed_at, created_at
FROM deploys
WHERE status = 'pending' AND deployed_at < sqlc.arg(cutoff)
ORDER BY deployed_at ASC;

-- name: PruneDeploys :execrows
DELETE FROM deploys WHERE deployed_at < sqlc.arg(cutoff);
