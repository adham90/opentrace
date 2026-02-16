-- Agent notes: persistent memory for the AI agent across sessions.
CREATE TABLE IF NOT EXISTS agent_notes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,  -- 'query', 'endpoint', 'service', 'healthcheck', 'error'
    entity_id   TEXT NOT NULL,  -- fingerprint, URL, query hash, etc.
    note        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_notes_entity ON agent_notes(entity_type, entity_id);
