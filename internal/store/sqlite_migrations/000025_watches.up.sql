-- Agent-first watches: simplified metric + operator + threshold monitoring.
-- Separate from the existing watchers system; both run side-by-side.

CREATE TABLE IF NOT EXISTS watches (
    id              TEXT PRIMARY KEY,
    metric          TEXT NOT NULL,           -- error_rate, response_time, p95_response, log_count, error_count, heartbeat, sql_count, cache_hit_rate
    operator        TEXT NOT NULL,           -- gt, gte, lt, lte, eq, neq
    threshold       REAL NOT NULL,
    service         TEXT NOT NULL DEFAULT '',
    endpoint        TEXT NOT NULL DEFAULT '',
    environment     TEXT NOT NULL DEFAULT '',
    commit_hash     TEXT NOT NULL DEFAULT '',
    duration        TEXT NOT NULL DEFAULT '1h',       -- how long the watch stays active
    urgency         TEXT NOT NULL DEFAULT 'normal',   -- low, normal, high, critical
    check_interval  TEXT NOT NULL DEFAULT '30s',
    baseline_window TEXT NOT NULL DEFAULT '1h',
    min_consecutive INTEGER NOT NULL DEFAULT 1,
    status          TEXT NOT NULL DEFAULT 'active',   -- active, triggered, resolved, expired
    baseline_json   TEXT,                              -- JSON: captured baseline snapshot
    consecutive_breaches INTEGER NOT NULL DEFAULT 0,
    current_value   REAL,
    expires_at      TEXT,                              -- RFC3339; NULL = no expiry
    created_by      TEXT NOT NULL DEFAULT '',          -- who created: agent session, user, etc.
    session_id      TEXT NOT NULL DEFAULT '',
    last_checked_at TEXT,
    next_check_at   TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_watches_status ON watches(status);
CREATE INDEX IF NOT EXISTS idx_watches_next_check ON watches(status, next_check_at);
CREATE INDEX IF NOT EXISTS idx_watches_service ON watches(service);
CREATE INDEX IF NOT EXISTS idx_watches_expires_at ON watches(expires_at);
CREATE INDEX IF NOT EXISTS idx_watches_session_id ON watches(session_id);

CREATE TABLE IF NOT EXISTS watch_runs (
    id           TEXT PRIMARY KEY,
    watch_id     TEXT NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'running',  -- running, completed, failed
    metric_value REAL,
    breached     INTEGER NOT NULL DEFAULT 0,       -- 0 or 1
    summary      TEXT,
    error_message TEXT,
    started_at   TEXT NOT NULL,
    finished_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_watch_runs_watch_id ON watch_runs(watch_id);

CREATE TABLE IF NOT EXISTS watch_alerts (
    id              TEXT PRIMARY KEY,
    watch_id        TEXT NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    run_id          TEXT REFERENCES watch_runs(id) ON DELETE SET NULL,
    urgency         TEXT NOT NULL DEFAULT 'normal',
    summary         TEXT NOT NULL,
    trigger_metric  TEXT NOT NULL,
    trigger_value   REAL NOT NULL,
    threshold_value REAL NOT NULL,
    evidence_json   TEXT,                            -- JSON: evidence bundle
    status          TEXT NOT NULL DEFAULT 'pending', -- pending, acknowledged, dismissed
    dismiss_reason  TEXT,
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_watch_alerts_watch_id ON watch_alerts(watch_id);
CREATE INDEX IF NOT EXISTS idx_watch_alerts_status ON watch_alerts(status);
