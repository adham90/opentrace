-- Events: generic CI/CD integration events.
-- Table: events (migration 000043)
-- NOTE: ListEvents uses dynamic WHERE clauses -- skip here, will use squirrel.

-- name: CreateEvent :one
INSERT INTO events (event_type, source, service, title, description,
                    metadata_json, external_id, external_url, author, created_at)
VALUES (sqlc.arg(event_type), sqlc.arg(source), sqlc.arg(service),
        sqlc.arg(title), sqlc.arg(description), sqlc.arg(metadata_json),
        sqlc.arg(external_id), sqlc.arg(external_url), sqlc.arg(author),
        sqlc.arg(created_at))
RETURNING *;

-- name: GetEventByID :one
SELECT id, event_type, source, service, title, description,
       metadata_json, external_id, external_url, author, created_at
FROM events WHERE id = sqlc.arg(id);

-- name: GetEventByExternalID :one
SELECT id, event_type, source, service, title, description,
       metadata_json, external_id, external_url, author, created_at
FROM events WHERE event_type = sqlc.arg(event_type) AND external_id = sqlc.arg(external_id);

-- name: PruneEvents :execrows
DELETE FROM events WHERE rowid IN (
  SELECT e.rowid FROM events e WHERE e.created_at < sqlc.arg(cutoff) LIMIT 1000
);
