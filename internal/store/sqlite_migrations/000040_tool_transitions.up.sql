-- Pre-computed tool transition statistics for ranking
CREATE TABLE IF NOT EXISTS tool_transitions (
    from_tool TEXT NOT NULL,
    to_tool TEXT NOT NULL,
    intent TEXT NOT NULL DEFAULT '',
    total_count INTEGER NOT NULL DEFAULT 0,
    resolved_count INTEGER NOT NULL DEFAULT 0,
    abandoned_count INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms INTEGER NOT NULL DEFAULT 0,
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (from_tool, to_tool, intent)
);

-- Curated workflow templates for cold start
CREATE TABLE IF NOT EXISTS workflow_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    intent TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    step_order INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    args_hint TEXT NOT NULL DEFAULT '{}',
    source TEXT NOT NULL DEFAULT 'curated' CHECK(source IN ('curated', 'learned')),
    resolved_session_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(intent, name, step_order)
);
CREATE INDEX IF NOT EXISTS idx_workflow_templates_intent ON workflow_templates(intent, step_order);

-- Suggestion acceptance tracking on existing mcp_activity
ALTER TABLE mcp_activity ADD COLUMN was_suggested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN suggestion_rank INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN followed_by TEXT NOT NULL DEFAULT '';
