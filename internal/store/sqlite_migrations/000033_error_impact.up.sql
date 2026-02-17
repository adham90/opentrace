-- Phase 3: Cohort-Based Error Impact
-- Track which users are affected by each error group
CREATE TABLE IF NOT EXISTS error_impacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    error_fingerprint TEXT NOT NULL,
    user_id TEXT NOT NULL,
    service TEXT NOT NULL DEFAULT '',

    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL DEFAULT 1,

    -- Context from the most recent occurrence (JSON: browser, platform, etc.)
    last_context TEXT NOT NULL DEFAULT '{}',
    last_log_id INTEGER NOT NULL DEFAULT 0,

    UNIQUE(error_fingerprint, user_id)
);

CREATE INDEX IF NOT EXISTS idx_error_impacts_fingerprint ON error_impacts(error_fingerprint);
CREATE INDEX IF NOT EXISTS idx_error_impacts_user ON error_impacts(user_id);
CREATE INDEX IF NOT EXISTS idx_error_impacts_last_seen ON error_impacts(last_seen_at DESC);

-- Extend error_groups with impact columns
ALTER TABLE error_groups ADD COLUMN unique_users INTEGER NOT NULL DEFAULT 0;
ALTER TABLE error_groups ADD COLUMN impact_score REAL NOT NULL DEFAULT 0;
ALTER TABLE error_groups ADD COLUMN common_context TEXT NOT NULL DEFAULT '{}';
