-- Phase 1: Trends Dashboard + Web Analytics

-- Pre-computed metric buckets for fast time-series charting
CREATE TABLE IF NOT EXISTS metric_buckets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bucket_start TEXT NOT NULL,
    bucket_interval TEXT NOT NULL,
    service TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT '',

    request_count INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    log_count INTEGER NOT NULL DEFAULT 0,

    avg_duration_ms REAL NOT NULL DEFAULT 0,
    p50_duration_ms REAL NOT NULL DEFAULT 0,
    p95_duration_ms REAL NOT NULL DEFAULT 0,
    p99_duration_ms REAL NOT NULL DEFAULT 0,
    max_duration_ms REAL NOT NULL DEFAULT 0,

    avg_sql_count REAL NOT NULL DEFAULT 0,
    avg_db_time_ms REAL NOT NULL DEFAULT 0,
    avg_cache_hit_ratio REAL NOT NULL DEFAULT 0,
    avg_http_external_ms REAL NOT NULL DEFAULT 0,

    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    UNIQUE(bucket_start, bucket_interval, service, endpoint, environment)
);

CREATE INDEX IF NOT EXISTS idx_metric_buckets_lookup
    ON metric_buckets(service, bucket_interval, bucket_start);

CREATE INDEX IF NOT EXISTS idx_metric_buckets_interval
    ON metric_buckets(bucket_interval, bucket_start);

-- Deploy markers extracted from commit_hash changes
CREATE TABLE IF NOT EXISTS deploy_markers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    commit_hash TEXT NOT NULL,
    first_seen_at TEXT NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 1,

    UNIQUE(service, environment, commit_hash)
);

CREATE INDEX IF NOT EXISTS idx_deploy_markers_service
    ON deploy_markers(service, first_seen_at);

-- Endpoint stats (materialized periodically)
CREATE TABLE IF NOT EXISTS endpoint_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    period TEXT NOT NULL,
    period_start TEXT NOT NULL,
    service TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT '',
    controller TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    path_pattern TEXT NOT NULL DEFAULT '',

    request_count INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    client_error_count INTEGER NOT NULL DEFAULT 0,

    avg_duration_ms REAL NOT NULL DEFAULT 0,
    p95_duration_ms REAL NOT NULL DEFAULT 0,
    max_duration_ms REAL NOT NULL DEFAULT 0,

    avg_sql_count REAL NOT NULL DEFAULT 0,

    status_2xx INTEGER NOT NULL DEFAULT 0,
    status_3xx INTEGER NOT NULL DEFAULT 0,
    status_4xx INTEGER NOT NULL DEFAULT 0,
    status_5xx INTEGER NOT NULL DEFAULT 0,

    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    UNIQUE(period, period_start, service, method, controller, action)
);

CREATE INDEX IF NOT EXISTS idx_endpoint_stats_lookup
    ON endpoint_stats(service, period, period_start);

-- Hourly traffic heatmap (24x7 grid)
CREATE TABLE IF NOT EXISTS traffic_heatmap (
    service TEXT NOT NULL DEFAULT '',
    day_of_week INTEGER NOT NULL,
    hour_of_day INTEGER NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms REAL NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    PRIMARY KEY(service, day_of_week, hour_of_day)
);
