-- Stage 6: Agent Assistant Experience
-- Extend investigation_sessions with development session tracking
ALTER TABLE investigation_sessions ADD COLUMN files_modified TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN files_read TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN linked_deploy_id INTEGER DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_inv_sessions_linked_deploy
    ON investigation_sessions(linked_deploy_id);

-- Generic events table for CI/CD integration
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL DEFAULT ''
        CHECK(event_type IN ('deploy', 'pr', 'test', 'alert', 'commit', 'custom')),
    source TEXT NOT NULL DEFAULT '',
    service TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    external_id TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_events_type_service ON events(event_type, service, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_external_id ON events(event_type, external_id);

-- Uncovered error paths (auto-populated from error_groups + code_entities)
CREATE TABLE IF NOT EXISTS uncovered_error_paths (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service TEXT NOT NULL DEFAULT '',
    error_fingerprint TEXT NOT NULL DEFAULT '',
    error_class TEXT NOT NULL DEFAULT '',
    source_file TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL DEFAULT '',
    error_count INTEGER NOT NULL DEFAULT 0,
    user_impact_score REAL NOT NULL DEFAULT 0.0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    priority_score REAL NOT NULL DEFAULT 0.0,
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_uncovered_paths_fingerprint
    ON uncovered_error_paths(error_fingerprint);
CREATE INDEX IF NOT EXISTS idx_uncovered_paths_priority
    ON uncovered_error_paths(service, priority_score DESC);
