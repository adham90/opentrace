-- Performance indexes for hot query paths

-- Server registration lookups by hostname
CREATE INDEX IF NOT EXISTS idx_servers_hostname ON servers(hostname);

-- Alert list queries joining on watcher_id
CREATE INDEX IF NOT EXISTS idx_alerts_watcher_id ON alerts(watcher_id);

-- Alert count queries filtering by dismissed status
CREATE INDEX IF NOT EXISTS idx_alerts_dismissed ON alerts(dismissed, created_at DESC);

-- Scheduler polling for due watchers
CREATE INDEX IF NOT EXISTS idx_watchers_status_next_run ON watchers(status, next_run_at);

-- Log filtering by environment
CREATE INDEX IF NOT EXISTS idx_logs_environment ON logs(environment);

-- Data source type lookups (ensureLogsConnector)
CREATE INDEX IF NOT EXISTS idx_data_sources_type ON data_sources(type);
