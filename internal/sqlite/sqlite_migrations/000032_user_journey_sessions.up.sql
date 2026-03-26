-- Phase 2: User Journey / Request Paths + Session Timeline
-- Add user_id and session_id to logs for fast lookups
ALTER TABLE logs ADD COLUMN user_id TEXT DEFAULT '';
ALTER TABLE logs ADD COLUMN session_id TEXT DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_logs_user_id ON logs(user_id) WHERE user_id != '';
CREATE INDEX IF NOT EXISTS idx_logs_session_id ON logs(session_id) WHERE session_id != '';

-- Backfill from metadata JSON (existing logs)
UPDATE logs SET user_id = json_extract(metadata, '$.user_id')
WHERE json_extract(metadata, '$.user_id') IS NOT NULL
  AND json_extract(metadata, '$.user_id') != ''
  AND user_id = '';

UPDATE logs SET session_id = json_extract(metadata, '$.session_id')
WHERE json_extract(metadata, '$.session_id') IS NOT NULL
  AND json_extract(metadata, '$.session_id') != ''
  AND session_id = '';

-- Pre-computed user sessions (rebuilt periodically by aggregator)
CREATE TABLE IF NOT EXISTS user_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    service TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT '',

    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    total_duration_ms REAL NOT NULL DEFAULT 0,

    -- Journey summary
    entry_path TEXT NOT NULL DEFAULT '',
    exit_path TEXT NOT NULL DEFAULT '',
    exit_status INTEGER NOT NULL DEFAULT 0,
    has_error INTEGER NOT NULL DEFAULT 0,

    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    UNIQUE(session_id, service)
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions(user_id) WHERE user_id != '';
CREATE INDEX IF NOT EXISTS idx_user_sessions_time ON user_sessions(started_at);
CREATE INDEX IF NOT EXISTS idx_user_sessions_errors ON user_sessions(has_error) WHERE has_error = 1;

-- Funnel definitions (user-created)
CREATE TABLE IF NOT EXISTS funnels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    service TEXT NOT NULL DEFAULT '',
    steps TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
