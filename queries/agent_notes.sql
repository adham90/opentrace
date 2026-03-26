-- Agent notes: persistent memory for the AI agent across sessions.
-- Allows the agent to remember findings about queries, endpoints, services, etc.

-- name: UpsertAgentNote :one
INSERT INTO agent_notes (entity_type, entity_id, note, created_at, updated_at)
VALUES (sqlc.arg(entity_type), sqlc.arg(entity_id), sqlc.arg(note), sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT(entity_type, entity_id) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at
RETURNING id, entity_type, entity_id, note, created_at, updated_at;

-- name: GetAgentNote :one
SELECT id, entity_type, entity_id, note, created_at, updated_at
FROM agent_notes
WHERE entity_type = sqlc.arg(entity_type) AND entity_id = sqlc.arg(entity_id);

-- name: ListAgentNotesByType :many
SELECT id, entity_type, entity_id, note, created_at, updated_at
FROM agent_notes
WHERE entity_type = sqlc.arg(entity_type)
ORDER BY updated_at DESC;

-- name: ListAllAgentNotes :many
SELECT id, entity_type, entity_id, note, created_at, updated_at
FROM agent_notes
ORDER BY updated_at DESC;

-- name: DeleteAgentNote :execrows
DELETE FROM agent_notes
WHERE entity_type = sqlc.arg(entity_type) AND entity_id = sqlc.arg(entity_id);

-- name: PruneAgentNotes :execrows
DELETE FROM agent_notes
WHERE updated_at < sqlc.arg(cutoff);
