-- Rename monitor_type column to watcher_type in watchers table.
ALTER TABLE watchers RENAME COLUMN monitor_type TO watcher_type;

-- Rename monitor columns in digests table.
ALTER TABLE digests RENAME COLUMN monitor_total TO watcher_total;
ALTER TABLE digests RENAME COLUMN monitor_errored TO watcher_errored;
