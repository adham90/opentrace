-- Health checks: simple HTTP endpoint monitoring (UptimeRobot-lite).
CREATE TABLE IF NOT EXISTS healthchecks (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    method          TEXT NOT NULL DEFAULT 'GET',
    interval_secs   INTEGER NOT NULL DEFAULT 60,
    timeout_secs    INTEGER NOT NULL DEFAULT 10,
    expected_status INTEGER NOT NULL DEFAULT 200,
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE IF NOT EXISTS healthcheck_results (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    healthcheck_id  TEXT NOT NULL REFERENCES healthchecks(id) ON DELETE CASCADE,
    status          TEXT NOT NULL CHECK(status IN ('up', 'down', 'degraded')),
    status_code     INTEGER,
    response_ms     INTEGER,
    error           TEXT NOT NULL DEFAULT '',
    checked_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_hc_results_id_time ON healthcheck_results(healthcheck_id, checked_at DESC);
