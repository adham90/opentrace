-- Query memory: stores explain_query findings across sessions
CREATE TABLE IF NOT EXISTS query_memory (
    fingerprint TEXT PRIMARY KEY,
    last_investigation_session_id TEXT NOT NULL DEFAULT '',
    investigation_count INTEGER NOT NULL DEFAULT 0,
    last_root_cause TEXT NOT NULL DEFAULT '',
    last_fix TEXT NOT NULL DEFAULT '',
    avg_duration_before_ms INTEGER DEFAULT NULL,
    avg_duration_after_ms INTEGER DEFAULT NULL,
    first_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Runbook effectiveness: tracks resolution rates per playbook
CREATE TABLE IF NOT EXISTS runbook_effectiveness (
    runbook_name TEXT PRIMARY KEY,
    total_executions INTEGER NOT NULL DEFAULT 0,
    resolved_sessions INTEGER NOT NULL DEFAULT 0,
    abandoned_sessions INTEGER NOT NULL DEFAULT 0,
    avg_steps_after INTEGER NOT NULL DEFAULT 0,
    avg_session_duration_seconds INTEGER NOT NULL DEFAULT 0,
    last_executed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
