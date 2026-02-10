-- Adaptive scheduling: state-aware frequency adjustment per monitor.
-- See plans/006-adaptive-scheduling.md
ALTER TABLE watchers ADD COLUMN adaptive_config TEXT;          -- JSON AdaptiveConfig (null = disabled)
ALTER TABLE watchers ADD COLUMN adaptive_state TEXT NOT NULL DEFAULT 'normal';  -- normal | escalated | sustained | relaxed | backing_off | error
ALTER TABLE watchers ADD COLUMN consecutive_clean_runs INTEGER NOT NULL DEFAULT 0;
ALTER TABLE watchers ADD COLUMN consecutive_errors INTEGER NOT NULL DEFAULT 0;
ALTER TABLE watchers ADD COLUMN escalated_at TEXT;             -- RFC3339 timestamp when escalation started
ALTER TABLE watchers ADD COLUMN base_time_range TEXT;          -- original time_range before adaptive override
