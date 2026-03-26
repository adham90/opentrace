ALTER TABLE data_sources ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE watchers ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN environment TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_data_sources_env ON data_sources(environment) WHERE environment != '';
CREATE INDEX IF NOT EXISTS idx_watchers_env ON watchers(environment) WHERE environment != '';
CREATE INDEX IF NOT EXISTS idx_alerts_env ON alerts(environment) WHERE environment != '';
