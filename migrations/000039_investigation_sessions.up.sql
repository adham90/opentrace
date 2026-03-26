-- Investigation sessions: tracks MCP investigation lifecycle with full identity chain.
CREATE TABLE IF NOT EXISTS investigation_sessions (
    id TEXT PRIMARY KEY,

    -- Identity (from auth token)
    user_id TEXT NOT NULL DEFAULT '',
    user_email TEXT NOT NULL DEFAULT '',
    user_role TEXT NOT NULL DEFAULT '',

    -- Client Info (from MCP initialize)
    client_name TEXT NOT NULL DEFAULT '',
    client_version TEXT NOT NULL DEFAULT '',
    workspace TEXT NOT NULL DEFAULT '',
    transport TEXT NOT NULL DEFAULT '',
    connection_id TEXT NOT NULL DEFAULT '',

    -- Session Classification
    intent TEXT NOT NULL DEFAULT '',
    intent_detail TEXT NOT NULL DEFAULT '',
    primary_service TEXT NOT NULL DEFAULT '',
    primary_datasource_id INTEGER DEFAULT NULL,

    -- Outcome
    status TEXT NOT NULL DEFAULT 'open'
        CHECK(status IN ('open', 'resolved', 'unresolved', 'abandoned')),
    summary TEXT NOT NULL DEFAULT '',
    root_cause TEXT NOT NULL DEFAULT '',
    fix_description TEXT NOT NULL DEFAULT '',

    -- Watcher Links
    created_watcher_ids TEXT NOT NULL DEFAULT '[]',
    triggered_by_alert_id TEXT DEFAULT NULL,
    triggered_by_watcher_id TEXT DEFAULT NULL,

    -- Error Group Links
    resolved_error_group_ids TEXT NOT NULL DEFAULT '[]',
    investigated_error_fingerprints TEXT NOT NULL DEFAULT '[]',

    -- Health Check Links
    created_healthcheck_ids TEXT NOT NULL DEFAULT '[]',
    triggered_by_healthcheck_id TEXT DEFAULT NULL,

    -- Agent Note Links
    created_note_ids TEXT NOT NULL DEFAULT '[]',
    auto_note_ids TEXT NOT NULL DEFAULT '[]',

    -- Runbook Links
    runbooks_executed TEXT NOT NULL DEFAULT '[]',

    -- Database Diagnostic Links
    explained_queries TEXT NOT NULL DEFAULT '[]',
    killed_queries TEXT NOT NULL DEFAULT '[]',

    -- Trace Links
    trace_ids TEXT NOT NULL DEFAULT '[]',

    -- Deploy Correlation
    correlated_deploy TEXT NOT NULL DEFAULT '',

    -- Metrics Snapshots
    pre_investigation_snapshot TEXT NOT NULL DEFAULT '{}',
    post_investigation_snapshot TEXT NOT NULL DEFAULT '{}',

    -- Metrics
    total_steps INTEGER NOT NULL DEFAULT 0,
    total_errors INTEGER NOT NULL DEFAULT 0,
    tool_sequence TEXT NOT NULL DEFAULT '[]',
    tool_fingerprint TEXT NOT NULL DEFAULT '',
    arg_signature TEXT NOT NULL DEFAULT '',

    -- Timing
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT DEFAULT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,

    -- Recurrence Tracking
    recurrence_group TEXT DEFAULT NULL,
    recurrence_count INTEGER NOT NULL DEFAULT 0,
    previous_session_id TEXT DEFAULT NULL,
    fix_durability_seconds INTEGER DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_inv_sessions_user ON investigation_sessions(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_status ON investigation_sessions(status, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_intent ON investigation_sessions(intent, intent_detail);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_service ON investigation_sessions(primary_service);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_connection ON investigation_sessions(connection_id);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_watcher ON investigation_sessions(triggered_by_watcher_id);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_healthcheck ON investigation_sessions(triggered_by_healthcheck_id);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_recurrence ON investigation_sessions(recurrence_group);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_fingerprint ON investigation_sessions(tool_fingerprint);
CREATE INDEX IF NOT EXISTS idx_inv_sessions_deploy ON investigation_sessions(correlated_deploy);

-- Enhance mcp_activity with investigation session linkage.
ALTER TABLE mcp_activity ADD COLUMN investigation_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_activity ADD COLUMN step_index INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_mcp_activity_inv_session
    ON mcp_activity(investigation_session_id, step_index);
