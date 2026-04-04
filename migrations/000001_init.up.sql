-- ============================================================================
-- OpenTrace Schema
-- NOTE: Log storage (logs, request_summaries, deep captures) is handled by
-- the segmented log store engine, NOT SQLite. Only platform, monitoring,
-- error tracking, analytics, and intelligence tables live here.
-- ============================================================================

-- ============================================================================
-- Platform
-- ============================================================================

CREATE TABLE IF NOT EXISTS users (
    id           TEXT PRIMARY KEY,
    email        TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    role         TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('admin', 'member')),
    mcp_enabled  INTEGER NOT NULL DEFAULT 0,
    mcp_token    TEXT UNIQUE,
    is_active    INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_mcp_token ON users(mcp_token) WHERE mcp_token IS NOT NULL;

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS app_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     TEXT NOT NULL,
    user_email  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    target_type TEXT,
    target_id   TEXT,
    details     TEXT,
    ip_address  TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_audit_log_user ON audit_log(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action);

CREATE TABLE IF NOT EXISTS jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    queue        TEXT NOT NULL DEFAULT 'default',
    job_type     TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending',
    priority     INTEGER NOT NULL DEFAULT 0,
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    run_at       TEXT NOT NULL DEFAULT (datetime('now')),
    started_at   TEXT,
    completed_at TEXT,
    last_error   TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_claim ON jobs(queue, status, run_at, priority);
CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(job_type);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);

-- ============================================================================
-- Data Sources & Infrastructure
-- ============================================================================

CREATE TABLE IF NOT EXISTS data_sources (
    id             TEXT PRIMARY KEY,
    type           TEXT NOT NULL CHECK(type IN ('logs', 'database', 'monitoring')),
    name           TEXT NOT NULL DEFAULT '',
    config         TEXT NOT NULL DEFAULT '{}',
    status         TEXT NOT NULL DEFAULT 'disconnected' CHECK(status IN ('connected', 'disconnected', 'error')),
    status_message TEXT,
    last_tested_at TEXT,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
    environment    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_data_sources_env ON data_sources(environment) WHERE environment != '';
CREATE INDEX IF NOT EXISTS idx_data_sources_type ON data_sources(type);

CREATE TABLE IF NOT EXISTS servers (
    id            TEXT PRIMARY KEY,
    hostname      TEXT NOT NULL,
    ip_address    TEXT,
    os            TEXT,
    arch          TEXT,
    agent_version TEXT,
    labels        TEXT DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'unknown',
    last_seen_at  TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    display_name  TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_servers_status ON servers(status);
CREATE INDEX IF NOT EXISTS idx_servers_hostname ON servers(hostname);

CREATE TABLE IF NOT EXISTS metrics (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id    TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    timestamp    TEXT NOT NULL,
    metric_name  TEXT NOT NULL,
    metric_value REAL NOT NULL,
    unit         TEXT,
    labels       TEXT DEFAULT '{}',
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_metrics_server_name_ts ON metrics(server_id, metric_name, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_metrics_ts ON metrics(timestamp);

-- ============================================================================
-- Error Tracking
-- ============================================================================

CREATE TABLE IF NOT EXISTS error_groups (
    fingerprint      TEXT PRIMARY KEY,
    service          TEXT NOT NULL,
    environment      TEXT NOT NULL DEFAULT '',
    exception_class  TEXT NOT NULL DEFAULT '',
    message          TEXT NOT NULL DEFAULT '',
    source_file      TEXT NOT NULL DEFAULT '',
    source_line      INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'unresolved'
        CHECK(status IN ('unresolved', 'resolved', 'ignored')),
    first_seen_at    TEXT NOT NULL,
    last_seen_at     TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    last_log_id      INTEGER DEFAULT 0,
    reopened_count   INTEGER NOT NULL DEFAULT 0,
    resolved_at      TEXT,
    ignored_at       TEXT,
    unique_users     INTEGER NOT NULL DEFAULT 0,
    impact_score     REAL NOT NULL DEFAULT 0,
    common_context   TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_error_groups_service ON error_groups(service, status);
CREATE INDEX IF NOT EXISTS idx_error_groups_last_seen ON error_groups(last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_error_groups_count ON error_groups(occurrence_count DESC);

CREATE TABLE IF NOT EXISTS error_group_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fingerprint TEXT NOT NULL REFERENCES error_groups(fingerprint) ON DELETE CASCADE,
    action      TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_error_group_events_fp ON error_group_events(fingerprint, created_at DESC);

CREATE TABLE IF NOT EXISTS error_impacts (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    error_fingerprint TEXT NOT NULL,
    user_id           TEXT NOT NULL,
    service           TEXT NOT NULL DEFAULT '',
    first_seen_at     TEXT NOT NULL,
    last_seen_at      TEXT NOT NULL,
    occurrence_count  INTEGER NOT NULL DEFAULT 1,
    last_context      TEXT NOT NULL DEFAULT '{}',
    last_log_id       INTEGER NOT NULL DEFAULT 0,

    UNIQUE(error_fingerprint, user_id)
);

CREATE INDEX IF NOT EXISTS idx_error_impacts_fingerprint ON error_impacts(error_fingerprint);
CREATE INDEX IF NOT EXISTS idx_error_impacts_user ON error_impacts(user_id);
CREATE INDEX IF NOT EXISTS idx_error_impacts_last_seen ON error_impacts(last_seen_at DESC);

-- ============================================================================
-- Monitoring
-- ============================================================================

CREATE TABLE IF NOT EXISTS healthchecks (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    method          TEXT NOT NULL DEFAULT 'GET',
    interval_secs   INTEGER NOT NULL DEFAULT 60,
    timeout_secs    INTEGER NOT NULL DEFAULT 10,
    expected_status INTEGER NOT NULL DEFAULT 200,
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    expected_body   TEXT NOT NULL DEFAULT '',
    retries         INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS healthcheck_results (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    healthcheck_id TEXT NOT NULL REFERENCES healthchecks(id) ON DELETE CASCADE,
    status         TEXT NOT NULL CHECK(status IN ('up', 'down', 'degraded')),
    status_code    INTEGER,
    response_ms    INTEGER,
    error          TEXT NOT NULL DEFAULT '',
    checked_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_hc_results_id_time ON healthcheck_results(healthcheck_id, checked_at DESC);

-- ============================================================================
-- Watches (agent-first alerting)
-- ============================================================================

CREATE TABLE IF NOT EXISTS watches (
    id                   TEXT PRIMARY KEY,
    conditions_json      TEXT NOT NULL,
    service              TEXT NOT NULL DEFAULT '',
    endpoint             TEXT NOT NULL DEFAULT '',
    environment          TEXT NOT NULL DEFAULT '',
    commit_hash          TEXT NOT NULL DEFAULT '',
    duration             TEXT NOT NULL DEFAULT '1h',
    urgency              TEXT NOT NULL DEFAULT 'normal',
    check_interval       TEXT NOT NULL DEFAULT '30s',
    baseline_window      TEXT NOT NULL DEFAULT '1h',
    min_consecutive      INTEGER NOT NULL DEFAULT 1,
    status               TEXT NOT NULL DEFAULT 'active',
    baseline_json        TEXT,
    consecutive_breaches INTEGER NOT NULL DEFAULT 0,
    current_value        REAL,
    expires_at           TEXT,
    created_by           TEXT NOT NULL DEFAULT '',
    session_id           TEXT NOT NULL DEFAULT '',
    last_checked_at      TEXT,
    next_check_at        TEXT,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_watches_status ON watches(status);
CREATE INDEX IF NOT EXISTS idx_watches_next_check ON watches(status, next_check_at);
CREATE INDEX IF NOT EXISTS idx_watches_service ON watches(service);
CREATE INDEX IF NOT EXISTS idx_watches_expires_at ON watches(expires_at);
CREATE INDEX IF NOT EXISTS idx_watches_session_id ON watches(session_id);

CREATE TABLE IF NOT EXISTS watch_runs (
    id           TEXT PRIMARY KEY,
    watch_id     TEXT NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'running',
    metric_value REAL,
    breached     INTEGER NOT NULL DEFAULT 0,
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
    evidence_json   TEXT,
    status          TEXT NOT NULL DEFAULT 'pending',
    dismiss_reason  TEXT,
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_watch_alerts_watch_id ON watch_alerts(watch_id);
CREATE INDEX IF NOT EXISTS idx_watch_alerts_status ON watch_alerts(status);

-- ============================================================================
-- Agent Notes
-- ============================================================================

CREATE TABLE IF NOT EXISTS agent_notes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    note        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_notes_entity ON agent_notes(entity_type, entity_id);

-- ============================================================================
-- Analytics (pre-aggregated — populated by background jobs)
-- ============================================================================

CREATE TABLE IF NOT EXISTS metric_buckets (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    bucket_start         TEXT NOT NULL,
    bucket_interval      TEXT NOT NULL,
    service              TEXT NOT NULL DEFAULT '',
    endpoint             TEXT NOT NULL DEFAULT '',
    environment          TEXT NOT NULL DEFAULT '',
    request_count        INTEGER NOT NULL DEFAULT 0,
    error_count          INTEGER NOT NULL DEFAULT 0,
    log_count            INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms      REAL NOT NULL DEFAULT 0,
    p50_duration_ms      REAL NOT NULL DEFAULT 0,
    p95_duration_ms      REAL NOT NULL DEFAULT 0,
    p99_duration_ms      REAL NOT NULL DEFAULT 0,
    max_duration_ms      REAL NOT NULL DEFAULT 0,
    avg_sql_count        REAL NOT NULL DEFAULT 0,
    avg_db_time_ms       REAL NOT NULL DEFAULT 0,
    avg_cache_hit_ratio  REAL NOT NULL DEFAULT 0,
    avg_http_external_ms REAL NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    UNIQUE(bucket_start, bucket_interval, service, endpoint, environment)
);

CREATE INDEX IF NOT EXISTS idx_metric_buckets_lookup ON metric_buckets(service, bucket_interval, bucket_start);
CREATE INDEX IF NOT EXISTS idx_metric_buckets_interval ON metric_buckets(bucket_interval, bucket_start);

CREATE TABLE IF NOT EXISTS deploy_markers (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    service       TEXT NOT NULL,
    environment   TEXT NOT NULL DEFAULT '',
    commit_hash   TEXT NOT NULL,
    first_seen_at TEXT NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 1,

    UNIQUE(service, environment, commit_hash)
);

CREATE INDEX IF NOT EXISTS idx_deploy_markers_service ON deploy_markers(service, first_seen_at);

CREATE TABLE IF NOT EXISTS endpoint_stats (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    period             TEXT NOT NULL,
    period_start       TEXT NOT NULL,
    service            TEXT NOT NULL DEFAULT '',
    method             TEXT NOT NULL DEFAULT '',
    controller         TEXT NOT NULL DEFAULT '',
    action             TEXT NOT NULL DEFAULT '',
    path_pattern       TEXT NOT NULL DEFAULT '',
    request_count      INTEGER NOT NULL DEFAULT 0,
    error_count        INTEGER NOT NULL DEFAULT 0,
    client_error_count INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms    REAL NOT NULL DEFAULT 0,
    p95_duration_ms    REAL NOT NULL DEFAULT 0,
    max_duration_ms    REAL NOT NULL DEFAULT 0,
    avg_sql_count      REAL NOT NULL DEFAULT 0,
    status_2xx         INTEGER NOT NULL DEFAULT 0,
    status_3xx         INTEGER NOT NULL DEFAULT 0,
    status_4xx         INTEGER NOT NULL DEFAULT 0,
    status_5xx         INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    UNIQUE(period, period_start, service, method, controller, action)
);

CREATE INDEX IF NOT EXISTS idx_endpoint_stats_lookup ON endpoint_stats(service, period, period_start);

CREATE TABLE IF NOT EXISTS traffic_heatmap (
    service         TEXT NOT NULL DEFAULT '',
    day_of_week     INTEGER NOT NULL,
    hour_of_day     INTEGER NOT NULL,
    request_count   INTEGER NOT NULL DEFAULT 0,
    error_count     INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms REAL NOT NULL DEFAULT 0,
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    PRIMARY KEY(service, day_of_week, hour_of_day)
);

-- ============================================================================
-- Tracing
-- ============================================================================

CREATE TABLE IF NOT EXISTS trace_status (
    trace_id        TEXT PRIMARY KEY,
    span_count      INTEGER NOT NULL DEFAULT 0,
    root_span_id    TEXT,
    services        TEXT NOT NULL DEFAULT '[]',
    first_seen_at   TEXT NOT NULL,
    last_updated_at TEXT NOT NULL,
    duration_ms     REAL NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'partial',
    has_errors      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_trace_status_last_updated ON trace_status(last_updated_at);
CREATE INDEX IF NOT EXISTS idx_trace_status_status ON trace_status(status);
CREATE INDEX IF NOT EXISTS idx_trace_status_first_seen ON trace_status(first_seen_at);

-- ============================================================================
-- MCP Activity
-- ============================================================================

CREATE TABLE IF NOT EXISTS mcp_activity (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id               TEXT NOT NULL,
    user_id                  TEXT,
    tool_name                TEXT NOT NULL,
    arguments                TEXT,
    result_preview           TEXT,
    is_error                 INTEGER NOT NULL DEFAULT 0,
    duration_ms              INTEGER,
    event_type               TEXT NOT NULL DEFAULT 'tool_call'
        CHECK(event_type IN ('tool_call', 'connect', 'disconnect')),
    created_at               TEXT NOT NULL DEFAULT (datetime('now')),
    investigation_session_id TEXT NOT NULL DEFAULT '',
    step_index               INTEGER NOT NULL DEFAULT 0,
    was_suggested            INTEGER NOT NULL DEFAULT 0,
    suggestion_rank          INTEGER NOT NULL DEFAULT 0,
    followed_by              TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_mcp_activity_created ON mcp_activity(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mcp_activity_session ON mcp_activity(session_id);
CREATE INDEX IF NOT EXISTS idx_mcp_activity_inv_session ON mcp_activity(investigation_session_id, step_index);

-- ============================================================================
-- Intelligence
-- ============================================================================

CREATE TABLE IF NOT EXISTS code_entities (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type          TEXT NOT NULL DEFAULT '' CHECK(entity_type IN ('file', 'controller', 'endpoint')),
    entity_name          TEXT NOT NULL DEFAULT '',
    service              TEXT NOT NULL DEFAULT '',
    risk_score           REAL NOT NULL DEFAULT 0.0,
    error_count          INTEGER NOT NULL DEFAULT 0,
    investigation_count  INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms      REAL DEFAULT NULL,
    last_error_at        TEXT DEFAULT NULL,
    last_investigation_at TEXT DEFAULT NULL,
    metadata_json        TEXT NOT NULL DEFAULT '{}',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_code_entities_type_name_service ON code_entities(entity_type, entity_name, service);
CREATE INDEX IF NOT EXISTS idx_code_entities_risk ON code_entities(service, risk_score DESC);

-- ============================================================================
-- Default Configuration
-- ============================================================================

INSERT OR IGNORE INTO app_config (key, value) VALUES ('pii_scrubbing', '{"enabled":true,"builtin":{"credit_cards":true,"emails":true,"phone_numbers":true,"ssn":true,"ip_addresses":false},"sensitive_fields":["password","token","secret","authorization","api_key"],"custom_patterns":[],"skip_domains":[],"skip_services":[]}');

INSERT OR IGNORE INTO app_config (key, value) VALUES ('retention_policy', '{"logs":"30d","error_groups":"never","metric_buckets":"180d","deploy_markers":"never"}');
