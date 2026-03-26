-- Track distributed trace reassembly status.
CREATE TABLE IF NOT EXISTS trace_status (
    trace_id       TEXT PRIMARY KEY,
    span_count     INTEGER NOT NULL DEFAULT 0,
    root_span_id   TEXT,
    services       TEXT NOT NULL DEFAULT '[]',  -- JSON array of distinct service names
    first_seen_at  TEXT NOT NULL,               -- RFC3339
    last_updated_at TEXT NOT NULL,              -- RFC3339
    duration_ms    REAL NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'partial',  -- 'partial', 'complete', 'timeout'
    has_errors     INTEGER NOT NULL DEFAULT 0        -- 1 if any span has level=error/fatal
);

CREATE INDEX idx_trace_status_last_updated ON trace_status(last_updated_at);
CREATE INDEX idx_trace_status_status ON trace_status(status);
CREATE INDEX idx_trace_status_first_seen ON trace_status(first_seen_at);
