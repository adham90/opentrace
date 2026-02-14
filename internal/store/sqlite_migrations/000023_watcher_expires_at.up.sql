-- pragma:foreign_keys_off
-- Recreate watchers table to update CHECK constraint (add 'expired' status)
-- and add new expires_at column. SQLite requires table recreation to modify
-- CHECK constraints. Foreign keys must be off during the table swap.

CREATE TABLE watchers_new (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT,
    severity TEXT NOT NULL DEFAULT 'warning' CHECK(severity IN ('info', 'warning', 'critical')),
    filters TEXT DEFAULT '{}',
    time_range TEXT NOT NULL DEFAULT '15m',
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'paused', 'error', 'expired')),
    notify TEXT DEFAULT '["dashboard"]',
    last_run_at TEXT,
    next_run_at TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    model TEXT NOT NULL DEFAULT '',
    effort TEXT NOT NULL DEFAULT 'medium',
    environment TEXT NOT NULL DEFAULT '',
    watcher_type TEXT NOT NULL DEFAULT 'ai',
    rule_config TEXT,
    data_source_id TEXT,
    schedule TEXT NOT NULL DEFAULT '',
    adaptive_config TEXT,
    adaptive_state TEXT NOT NULL DEFAULT 'normal',
    consecutive_clean_runs INTEGER NOT NULL DEFAULT 0,
    consecutive_errors INTEGER NOT NULL DEFAULT 0,
    escalated_at TEXT,
    base_time_range TEXT,
    human_summary TEXT,
    type_config TEXT,
    expires_at TEXT
);

INSERT INTO watchers_new SELECT *, NULL FROM watchers;

DROP TABLE watchers;

ALTER TABLE watchers_new RENAME TO watchers;

-- Recreate indexes (dropped with the old table)
CREATE INDEX IF NOT EXISTS idx_watchers_env ON watchers(environment) WHERE environment != '';
CREATE INDEX IF NOT EXISTS idx_watchers_monitor_type ON watchers(watcher_type);
CREATE INDEX IF NOT EXISTS idx_watchers_status_next_run ON watchers(status, next_run_at);
