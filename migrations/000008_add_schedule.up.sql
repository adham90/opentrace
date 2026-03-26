-- Add schedule column for cron expression support.
-- Empty string means "use time_range as schedule" (backward compat).
-- When set, schedule controls when the monitor runs; time_range controls log lookback.
ALTER TABLE watchers ADD COLUMN schedule TEXT NOT NULL DEFAULT '';
