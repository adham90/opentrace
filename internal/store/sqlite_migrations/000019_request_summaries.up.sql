CREATE TABLE IF NOT EXISTS request_summaries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id INTEGER NOT NULL REFERENCES logs(id) ON DELETE CASCADE,

    -- Request identity
    controller TEXT,
    action TEXT,
    method TEXT,
    path TEXT,
    status INTEGER,

    -- Timing
    duration_ms REAL,
    db_time_ms REAL,
    view_time_ms REAL,

    -- SQL metrics
    sql_count INTEGER DEFAULT 0,
    sql_total_ms REAL DEFAULT 0,
    sql_slowest_ms REAL DEFAULT 0,
    sql_slowest_name TEXT,
    n_plus_one INTEGER DEFAULT 0,

    -- View metrics
    view_count INTEGER DEFAULT 0,
    view_total_ms REAL DEFAULT 0,
    view_slowest_ms REAL DEFAULT 0,
    view_slowest_template TEXT,

    -- Cache metrics
    cache_reads INTEGER DEFAULT 0,
    cache_hits INTEGER DEFAULT 0,
    cache_writes INTEGER DEFAULT 0,
    cache_hit_ratio REAL,

    -- External HTTP metrics
    http_external_count INTEGER DEFAULT 0,
    http_external_total_ms REAL DEFAULT 0,
    http_slowest_ms REAL DEFAULT 0,
    http_slowest_host TEXT,

    -- Memory
    memory_before_mb REAL,
    memory_after_mb REAL,
    memory_delta_mb REAL,

    -- Timeline (JSON array, only loaded when needed)
    timeline TEXT,

    -- Timestamps
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_req_summary_log_id ON request_summaries(log_id);
CREATE INDEX idx_req_summary_controller ON request_summaries(controller, action);
CREATE INDEX idx_req_summary_duration ON request_summaries(duration_ms);
CREATE INDEX idx_req_summary_sql_count ON request_summaries(sql_count);
CREATE INDEX idx_req_summary_status ON request_summaries(status);
CREATE INDEX idx_req_summary_created ON request_summaries(created_at);
CREATE INDEX idx_req_summary_n_plus_one ON request_summaries(n_plus_one) WHERE n_plus_one = 1;
