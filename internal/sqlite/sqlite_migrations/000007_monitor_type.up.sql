-- Add monitor_type to watchers table (default 'ai' preserves existing behavior)
ALTER TABLE watchers ADD COLUMN monitor_type TEXT NOT NULL DEFAULT 'ai';

-- Rule configuration (JSON) — only used when monitor_type = 'rule'
ALTER TABLE watchers ADD COLUMN rule_config TEXT;

-- Data source ID for query/health monitors (FK to data_sources)
ALTER TABLE watchers ADD COLUMN data_source_id TEXT;

-- Index for quick lookup by type
CREATE INDEX idx_watchers_monitor_type ON watchers(monitor_type);
