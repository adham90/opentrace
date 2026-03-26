-- Error groups: aggregate errors by fingerprint (Sentry-lite).
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
    ignored_at       TEXT
);
CREATE INDEX IF NOT EXISTS idx_error_groups_service ON error_groups(service, status);
CREATE INDEX IF NOT EXISTS idx_error_groups_last_seen ON error_groups(last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_error_groups_count ON error_groups(occurrence_count DESC);

-- Audit trail for error group lifecycle events.
CREATE TABLE IF NOT EXISTS error_group_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fingerprint TEXT NOT NULL REFERENCES error_groups(fingerprint) ON DELETE CASCADE,
    action      TEXT NOT NULL,  -- 'resolved', 'ignored', 'reopened'
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_error_group_events_fp ON error_group_events(fingerprint, created_at DESC);
