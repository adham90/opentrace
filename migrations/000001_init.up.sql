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
-- Data Sources
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
-- Logging
-- ============================================================================

CREATE TABLE IF NOT EXISTS logs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp         TEXT NOT NULL,
    level             TEXT NOT NULL DEFAULT 'info',
    service           TEXT NOT NULL DEFAULT '',
    trace_id          TEXT,
    message           TEXT NOT NULL DEFAULT '',
    environment       TEXT,
    metadata          TEXT DEFAULT '{}',
    event_type        TEXT NOT NULL DEFAULT '',
    span_id           TEXT,
    parent_span_id    TEXT,
    commit_hash       TEXT,
    request_id        TEXT,
    exception_class   TEXT,
    error_fingerprint TEXT,
    source_file       TEXT,
    source_line       INTEGER,
    user_id           TEXT DEFAULT ''
);

-- Compound indexes (cover all query patterns — most queries filter by timestamp + service/level)
CREATE INDEX IF NOT EXISTS idx_logs_service_timestamp ON logs(service, timestamp);
CREATE INDEX IF NOT EXISTS idx_logs_level_timestamp ON logs(level, timestamp);
CREATE INDEX IF NOT EXISTS idx_logs_service_level_timestamp ON logs(service, level, timestamp);
CREATE INDEX IF NOT EXISTS idx_logs_event_type_timestamp ON logs(event_type, timestamp) WHERE event_type != '';

-- Point-lookup indexes (trace assembly, request lookup, error investigation)
CREATE INDEX IF NOT EXISTS idx_logs_trace_id ON logs(trace_id) WHERE trace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_logs_span_id ON logs(span_id) WHERE span_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_logs_parent_span_id ON logs(parent_span_id) WHERE parent_span_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_logs_commit_hash ON logs(commit_hash) WHERE commit_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_logs_request_id ON logs(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_logs_error_fingerprint ON logs(error_fingerprint) WHERE error_fingerprint IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_logs_user_id ON logs(user_id) WHERE user_id != '';

-- Sparse indexes (rarely filtered alone, but cheap since partial)
CREATE INDEX IF NOT EXISTS idx_logs_environment ON logs(environment) WHERE environment != '';
CREATE INDEX IF NOT EXISTS idx_logs_exception_class ON logs(exception_class) WHERE exception_class IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_logs_source_file ON logs(source_file) WHERE source_file IS NOT NULL;

-- FTS5 virtual table for full-text search on log messages
CREATE VIRTUAL TABLE IF NOT EXISTS logs_fts USING fts5(message, content=logs, content_rowid=id);

CREATE TRIGGER IF NOT EXISTS logs_ai AFTER INSERT ON logs BEGIN
    INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
END;

CREATE TRIGGER IF NOT EXISTS logs_ad AFTER DELETE ON logs BEGIN
    INSERT INTO logs_fts(logs_fts, rowid, message) VALUES('delete', old.id, old.message);
END;

CREATE TRIGGER IF NOT EXISTS logs_au AFTER UPDATE ON logs BEGIN
    INSERT INTO logs_fts(logs_fts, rowid, message) VALUES('delete', old.id, old.message);
    INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
END;

CREATE TABLE IF NOT EXISTS ingest_batches (
    batch_id    TEXT PRIMARY KEY,
    log_count   INTEGER NOT NULL,
    received_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_ingest_batches_received ON ingest_batches(received_at);

CREATE TABLE IF NOT EXISTS request_summaries (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id               INTEGER NOT NULL REFERENCES logs(id) ON DELETE CASCADE,

    -- Request identity
    controller           TEXT,
    action               TEXT,
    method               TEXT,
    path                 TEXT,
    status               INTEGER,

    -- Timing
    duration_ms          REAL,
    db_time_ms           REAL,
    view_time_ms         REAL,

    -- SQL metrics
    sql_count            INTEGER DEFAULT 0,
    sql_total_ms         REAL DEFAULT 0,
    sql_slowest_ms       REAL DEFAULT 0,
    sql_slowest_name     TEXT,
    n_plus_one           INTEGER DEFAULT 0,

    -- View metrics
    view_count           INTEGER DEFAULT 0,
    view_total_ms        REAL DEFAULT 0,
    view_slowest_ms      REAL DEFAULT 0,
    view_slowest_template TEXT,

    -- Cache metrics
    cache_reads          INTEGER DEFAULT 0,
    cache_hits           INTEGER DEFAULT 0,
    cache_writes         INTEGER DEFAULT 0,
    cache_hit_ratio      REAL,

    -- External HTTP metrics
    http_external_count  INTEGER DEFAULT 0,
    http_external_total_ms REAL DEFAULT 0,
    http_slowest_ms      REAL DEFAULT 0,
    http_slowest_host    TEXT,

    -- Memory
    memory_before_mb     REAL,
    memory_after_mb      REAL,
    memory_delta_mb      REAL,

    -- Timeline (JSON array, only loaded when needed)
    timeline             TEXT,

    -- Timestamps
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    -- Duplicate query tracking
    time_breakdown       TEXT,
    duplicate_queries    INTEGER DEFAULT 0,
    worst_duplicate_count INTEGER DEFAULT 0,
    top_duplicates       TEXT
);

CREATE INDEX IF NOT EXISTS idx_req_summary_log_id ON request_summaries(log_id);
CREATE INDEX IF NOT EXISTS idx_req_summary_controller ON request_summaries(controller, action);
CREATE INDEX IF NOT EXISTS idx_req_summary_duration ON request_summaries(duration_ms);
CREATE INDEX IF NOT EXISTS idx_req_summary_sql_count ON request_summaries(sql_count);
CREATE INDEX IF NOT EXISTS idx_req_summary_status ON request_summaries(status);
CREATE INDEX IF NOT EXISTS idx_req_summary_created ON request_summaries(created_at);
CREATE INDEX IF NOT EXISTS idx_req_summary_n_plus_one ON request_summaries(n_plus_one) WHERE n_plus_one = 1;

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
    last_log_id      INTEGER REFERENCES logs(id),
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
-- Analytics
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
-- Journeys
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_sessions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        TEXT NOT NULL,
    user_id           TEXT NOT NULL DEFAULT '',
    service           TEXT NOT NULL DEFAULT '',
    environment       TEXT NOT NULL DEFAULT '',
    started_at        TEXT NOT NULL,
    ended_at          TEXT NOT NULL,
    request_count     INTEGER NOT NULL DEFAULT 0,
    error_count       INTEGER NOT NULL DEFAULT 0,
    total_duration_ms REAL NOT NULL DEFAULT 0,
    entry_path        TEXT NOT NULL DEFAULT '',
    exit_path         TEXT NOT NULL DEFAULT '',
    exit_status       INTEGER NOT NULL DEFAULT 0,
    has_error         INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    UNIQUE(session_id, service)
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions(user_id) WHERE user_id != '';
CREATE INDEX IF NOT EXISTS idx_user_sessions_time ON user_sessions(started_at);
CREATE INDEX IF NOT EXISTS idx_user_sessions_errors ON user_sessions(has_error) WHERE has_error = 1;
CREATE INDEX IF NOT EXISTS idx_user_sessions_user_service_started ON user_sessions(user_id, service, started_at DESC);

CREATE TABLE IF NOT EXISTS funnels (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    service    TEXT NOT NULL DEFAULT '',
    steps      TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
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
-- Investigation Memory
-- ============================================================================

CREATE TABLE IF NOT EXISTS investigation_sessions (
    id                              TEXT PRIMARY KEY,

    -- Identity
    user_id                         TEXT NOT NULL DEFAULT '',
    user_email                      TEXT NOT NULL DEFAULT '',
    user_role                       TEXT NOT NULL DEFAULT '',

    -- Client Info
    client_name                     TEXT NOT NULL DEFAULT '',
    client_version                  TEXT NOT NULL DEFAULT '',
    workspace                       TEXT NOT NULL DEFAULT '',
    transport                       TEXT NOT NULL DEFAULT '',
    connection_id                   TEXT NOT NULL DEFAULT '',

    -- Session Classification
    intent                          TEXT NOT NULL DEFAULT '',
    intent_detail                   TEXT NOT NULL DEFAULT '',
    primary_service                 TEXT NOT NULL DEFAULT '',
    primary_datasource_id           INTEGER DEFAULT NULL,

    -- Outcome
    status                          TEXT NOT NULL DEFAULT 'open'
        CHECK(status IN ('open', 'resolved', 'unresolved', 'abandoned')),
    summary                         TEXT NOT NULL DEFAULT '',
    root_cause                      TEXT NOT NULL DEFAULT '',
    fix_description                 TEXT NOT NULL DEFAULT '',

    -- Watcher Links
    created_watcher_ids             TEXT NOT NULL DEFAULT '[]',
    triggered_by_alert_id           TEXT DEFAULT NULL,
    triggered_by_watcher_id         TEXT DEFAULT NULL,

    -- Error Group Links
    resolved_error_group_ids        TEXT NOT NULL DEFAULT '[]',
    investigated_error_fingerprints TEXT NOT NULL DEFAULT '[]',

    -- Health Check Links
    created_healthcheck_ids         TEXT NOT NULL DEFAULT '[]',
    triggered_by_healthcheck_id     TEXT DEFAULT NULL,

    -- Agent Note Links
    created_note_ids                TEXT NOT NULL DEFAULT '[]',
    auto_note_ids                   TEXT NOT NULL DEFAULT '[]',

    -- Runbook Links
    runbooks_executed               TEXT NOT NULL DEFAULT '[]',

    -- Database Diagnostic Links
    explained_queries               TEXT NOT NULL DEFAULT '[]',
    killed_queries                  TEXT NOT NULL DEFAULT '[]',

    -- Trace Links
    trace_ids                       TEXT NOT NULL DEFAULT '[]',

    -- Deploy Correlation
    correlated_deploy               TEXT NOT NULL DEFAULT '',

    -- Metrics Snapshots
    pre_investigation_snapshot      TEXT NOT NULL DEFAULT '{}',
    post_investigation_snapshot     TEXT NOT NULL DEFAULT '{}',

    -- Metrics
    total_steps                     INTEGER NOT NULL DEFAULT 0,
    total_errors                    INTEGER NOT NULL DEFAULT 0,
    tool_sequence                   TEXT NOT NULL DEFAULT '[]',
    tool_fingerprint                TEXT NOT NULL DEFAULT '',
    arg_signature                   TEXT NOT NULL DEFAULT '',

    -- Timing
    started_at                      TEXT NOT NULL DEFAULT (datetime('now')),
    last_activity_at                TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at                        TEXT DEFAULT NULL,
    duration_seconds                INTEGER NOT NULL DEFAULT 0,

    -- Recurrence Tracking
    recurrence_group                TEXT DEFAULT NULL,
    recurrence_count                INTEGER NOT NULL DEFAULT 0,
    previous_session_id             TEXT DEFAULT NULL,
    fix_durability_seconds          INTEGER DEFAULT NULL,

    -- File Links
    files_modified                  TEXT NOT NULL DEFAULT '[]',
    files_read                      TEXT NOT NULL DEFAULT '[]',
    linked_deploy_id                INTEGER DEFAULT NULL
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
CREATE INDEX IF NOT EXISTS idx_inv_sessions_linked_deploy ON investigation_sessions(linked_deploy_id);

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

CREATE TABLE IF NOT EXISTS tool_transitions (
    from_tool       TEXT NOT NULL,
    to_tool         TEXT NOT NULL,
    intent          TEXT NOT NULL DEFAULT '',
    total_count     INTEGER NOT NULL DEFAULT 0,
    resolved_count  INTEGER NOT NULL DEFAULT 0,
    abandoned_count INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms INTEGER NOT NULL DEFAULT 0,
    last_seen_at    TEXT NOT NULL DEFAULT (datetime('now')),

    PRIMARY KEY (from_tool, to_tool, intent)
);

CREATE TABLE IF NOT EXISTS workflow_templates (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    intent                 TEXT NOT NULL,
    name                   TEXT NOT NULL DEFAULT '',
    step_order             INTEGER NOT NULL,
    tool_name              TEXT NOT NULL,
    args_hint              TEXT NOT NULL DEFAULT '{}',
    source                 TEXT NOT NULL DEFAULT 'curated' CHECK(source IN ('curated', 'learned')),
    resolved_session_count INTEGER NOT NULL DEFAULT 0,
    created_at             TEXT NOT NULL DEFAULT (datetime('now')),

    UNIQUE(intent, name, step_order)
);

CREATE INDEX IF NOT EXISTS idx_workflow_templates_intent ON workflow_templates(intent, step_order);

CREATE TABLE IF NOT EXISTS query_memory (
    fingerprint                  TEXT PRIMARY KEY,
    last_investigation_session_id TEXT NOT NULL DEFAULT '',
    investigation_count          INTEGER NOT NULL DEFAULT 0,
    last_root_cause              TEXT NOT NULL DEFAULT '',
    last_fix                     TEXT NOT NULL DEFAULT '',
    avg_duration_before_ms       INTEGER DEFAULT NULL,
    avg_duration_after_ms        INTEGER DEFAULT NULL,
    first_seen_at                TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at                 TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS runbook_effectiveness (
    runbook_name                 TEXT PRIMARY KEY,
    total_executions             INTEGER NOT NULL DEFAULT 0,
    resolved_sessions            INTEGER NOT NULL DEFAULT 0,
    abandoned_sessions           INTEGER NOT NULL DEFAULT 0,
    avg_steps_after              INTEGER NOT NULL DEFAULT 0,
    avg_session_duration_seconds INTEGER NOT NULL DEFAULT 0,
    last_executed_at             TEXT NOT NULL DEFAULT (datetime('now'))
);

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

CREATE TABLE IF NOT EXISTS deploys (
    id                           INTEGER PRIMARY KEY AUTOINCREMENT,
    service                      TEXT NOT NULL DEFAULT '',
    environment                  TEXT NOT NULL DEFAULT '',
    commit_hash                  TEXT NOT NULL DEFAULT '',
    branch                       TEXT NOT NULL DEFAULT '',
    author                       TEXT NOT NULL DEFAULT '',
    files_changed_json           TEXT NOT NULL DEFAULT '[]',
    deploy_source                TEXT NOT NULL DEFAULT 'webhook'
        CHECK(deploy_source IN ('webhook', 'auto-detected', 'manual')),
    pre_error_rate               REAL DEFAULT NULL,
    post_error_rate              REAL DEFAULT NULL,
    pre_avg_duration_ms          REAL DEFAULT NULL,
    post_avg_duration_ms         REAL DEFAULT NULL,
    impact_measured_at           TEXT DEFAULT NULL,
    linked_investigation_ids_json TEXT NOT NULL DEFAULT '[]',
    status                       TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending', 'measured', 'incident')),
    deployed_at                  TEXT NOT NULL DEFAULT (datetime('now')),
    created_at                   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_deploys_service ON deploys(service, deployed_at DESC);
CREATE INDEX IF NOT EXISTS idx_deploys_commit ON deploys(commit_hash);
CREATE INDEX IF NOT EXISTS idx_deploys_status ON deploys(status, deployed_at DESC);

CREATE TABLE IF NOT EXISTS events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type    TEXT NOT NULL DEFAULT ''
        CHECK(event_type IN ('deploy', 'pr', 'test', 'alert', 'commit', 'custom')),
    source        TEXT NOT NULL DEFAULT '',
    service       TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    external_id   TEXT NOT NULL DEFAULT '',
    external_url  TEXT NOT NULL DEFAULT '',
    author        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_events_type_service ON events(event_type, service, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_external_id ON events(event_type, external_id);

CREATE TABLE IF NOT EXISTS uncovered_error_paths (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    service              TEXT NOT NULL DEFAULT '',
    error_fingerprint    TEXT NOT NULL DEFAULT '',
    error_class          TEXT NOT NULL DEFAULT '',
    source_file          TEXT NOT NULL DEFAULT '',
    endpoint             TEXT NOT NULL DEFAULT '',
    error_count          INTEGER NOT NULL DEFAULT 0,
    user_impact_score    REAL NOT NULL DEFAULT 0.0,
    investigation_count  INTEGER NOT NULL DEFAULT 0,
    priority_score       REAL NOT NULL DEFAULT 0.0,
    last_seen_at         TEXT NOT NULL DEFAULT (datetime('now')),
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_uncovered_paths_fingerprint ON uncovered_error_paths(error_fingerprint);
CREATE INDEX IF NOT EXISTS idx_uncovered_paths_priority ON uncovered_error_paths(service, priority_score DESC);
