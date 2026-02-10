-- Add digests table for storing generated health digests.
-- Key summary fields are promoted to columns for efficient querying.
-- Full digest JSON is stored in the data column for detail views.
CREATE TABLE IF NOT EXISTS digests (
    id TEXT PRIMARY KEY,
    environment TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'healthy',
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,
    alert_total INTEGER NOT NULL DEFAULT 0,
    alert_critical INTEGER NOT NULL DEFAULT 0,
    alert_warning INTEGER NOT NULL DEFAULT 0,
    monitor_total INTEGER NOT NULL DEFAULT 0,
    monitor_errored INTEGER NOT NULL DEFAULT 0,
    failed_runs INTEGER NOT NULL DEFAULT 0,
    data TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_digests_period ON digests(period_end DESC);
CREATE INDEX idx_digests_env ON digests(environment, period_end DESC);
CREATE INDEX idx_digests_status ON digests(status, period_end DESC);
