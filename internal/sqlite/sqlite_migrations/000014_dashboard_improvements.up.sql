-- Phase 1: MCP activity tracking
CREATE TABLE IF NOT EXISTS mcp_activity (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    user_id TEXT,
    tool_name TEXT NOT NULL,
    arguments TEXT,
    result_preview TEXT,
    is_error INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER,
    event_type TEXT NOT NULL DEFAULT 'tool_call'
        CHECK(event_type IN ('tool_call', 'connect', 'disconnect')),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_mcp_activity_created ON mcp_activity(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mcp_activity_session ON mcp_activity(session_id);

-- Phase 4: Investigation run tracking
ALTER TABLE watcher_runs ADD COLUMN parent_alert_id TEXT;
ALTER TABLE watcher_runs ADD COLUMN run_type TEXT NOT NULL DEFAULT 'scheduled';

-- Phase 5: Alert groups (incidents)
CREATE TABLE IF NOT EXISTS alert_groups (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open'
        CHECK(status IN ('open', 'investigating', 'resolved', 'dismissed')),
    severity TEXT NOT NULL DEFAULT 'warning',
    environment TEXT NOT NULL DEFAULT '',
    root_cause TEXT,
    resolution TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at TEXT
);

CREATE TABLE IF NOT EXISTS alert_group_members (
    group_id TEXT NOT NULL REFERENCES alert_groups(id) ON DELETE CASCADE,
    alert_id TEXT NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, alert_id)
);
CREATE INDEX IF NOT EXISTS idx_alert_group_members_alert ON alert_group_members(alert_id);
