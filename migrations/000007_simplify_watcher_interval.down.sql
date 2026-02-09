ALTER TABLE watchers ADD COLUMN interval_seconds INT NOT NULL DEFAULT 300;
ALTER TABLE watchers DROP COLUMN time_range;
