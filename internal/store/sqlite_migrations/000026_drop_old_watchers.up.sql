-- Drop the old watcher/alert system tables (replaced by watches).
-- Order matters: children first to satisfy foreign key constraints.

DROP TABLE IF EXISTS alert_group_members;
DROP TABLE IF EXISTS alert_groups;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS watcher_runs;
DROP TABLE IF EXISTS watchers;

-- Remove auto_update setting (self-update system removed).
DELETE FROM app_config WHERE key = 'auto_update';
