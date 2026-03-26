-- Journeys: user session tracking, funnels, and request timelines.
-- Queries for user_sessions, funnels, and journey-related log/request_summary joins.

-- name: GetSession :one
SELECT id, session_id, user_id, service, environment,
       started_at, ended_at, request_count, error_count, total_duration_ms,
       entry_path, exit_path, exit_status, has_error, created_at
FROM user_sessions
WHERE session_id = sqlc.arg(session_id);

-- name: GetSessionRequests :many
SELECT l.timestamp, COALESCE(rs.controller, '') AS controller, COALESCE(rs."action", '') AS "action",
       COALESCE(rs.path, '') AS path, COALESCE(rs.method, '') AS method, COALESCE(rs.status, 0) AS status,
       COALESCE(rs.duration_ms, 0) AS duration_ms, COALESCE(rs.sql_count, 0) AS sql_count,
       CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END AS has_error,
       COALESCE(l.exception_class, '') AS exception_class,
       COALESCE(l.request_id, '') AS request_id, l.id AS log_id
FROM logs l
JOIN request_summaries rs ON rs.log_id = l.id
WHERE l.session_id = sqlc.arg(session_id)
ORDER BY l.timestamp ASC;

-- name: CreateFunnel :exec
INSERT INTO funnels (name, service, steps, created_at, updated_at)
VALUES (sqlc.arg(name), sqlc.arg(service), sqlc.arg(steps), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetFunnel :one
SELECT id, name, service, steps, created_at, updated_at
FROM funnels WHERE id = sqlc.arg(id);

-- name: ListFunnels :many
SELECT id, name, service, steps, created_at, updated_at
FROM funnels ORDER BY created_at DESC;

-- name: DeleteFunnel :execrows
DELETE FROM funnels WHERE id = sqlc.arg(id);

-- name: GetRequestTimeline :one
SELECT l.id AS log_id, COALESCE(l.request_id, '') AS request_id,
       COALESCE(rs.controller, '') AS controller, COALESCE(rs."action", '') AS "action",
       COALESCE(rs.path, '') AS path,
       COALESCE(rs.status, 0) AS status, COALESCE(rs.duration_ms, 0) AS duration_ms,
       l.timestamp, rs.timeline
FROM logs l
JOIN request_summaries rs ON rs.log_id = l.id
WHERE l.id = sqlc.arg(log_id);

-- name: GetSessionTimeline :many
SELECT l.id AS log_id, COALESCE(l.request_id, '') AS request_id,
       COALESCE(rs.controller, '') AS controller, COALESCE(rs."action", '') AS "action",
       COALESCE(rs.path, '') AS path,
       COALESCE(rs.status, 0) AS status, COALESCE(rs.duration_ms, 0) AS duration_ms,
       l.timestamp, rs.timeline
FROM logs l
JOIN request_summaries rs ON rs.log_id = l.id
WHERE l.session_id = sqlc.arg(session_id)
ORDER BY l.timestamp ASC;

-- DYNAMIC: use squirrel for BuildSessions (complex aggregation with subqueries + upsert loop)
-- DYNAMIC: use squirrel for ListSessions (optional user_id/service/has_error/since/until filters)
-- DYNAMIC: use squirrel for GetUserJourney (user_id + since + limit)
-- DYNAMIC: use squirrel for CommonPaths (application-level path extraction + counting)
-- DYNAMIC: use squirrel for AnalyzeFunnel (dynamic service filter + application-level funnel counting)
