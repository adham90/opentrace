-- Alert dismiss feedback
ALTER TABLE alerts ADD COLUMN dismiss_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN dismissed_at TEXT;
ALTER TABLE alerts ADD COLUMN snoozed_until TEXT;

-- Watcher human summary (Claude-generated plain-English explanation)
ALTER TABLE watchers ADD COLUMN human_summary TEXT;

-- Index for effectiveness queries
CREATE INDEX IF NOT EXISTS idx_alerts_watcher_dismissed ON alerts(watcher_id, dismissed, dismiss_reason);
