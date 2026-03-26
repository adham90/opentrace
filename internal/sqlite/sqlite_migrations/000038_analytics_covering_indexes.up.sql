-- Composite covering indexes for common analytics aggregation queries.
-- These speed up queries in analytics_store.go and log_store.go that filter
-- and group by (service, timestamp), (level, timestamp), and (event_type, timestamp).

-- CountByLevel, CountByService, and TrafficSummary: filter by service + timestamp range
CREATE INDEX IF NOT EXISTS idx_logs_service_timestamp ON logs(service, timestamp);

-- CountByLevel: filter by level + timestamp range
CREATE INDEX IF NOT EXISTS idx_logs_level_timestamp ON logs(level, timestamp);

-- Combined service + level + timestamp for queries that filter on both
CREATE INDEX IF NOT EXISTS idx_logs_service_level_timestamp ON logs(service, level, timestamp);

-- DistinctValues and search by event_type + timestamp range
CREATE INDEX IF NOT EXISTS idx_logs_event_type_timestamp ON logs(event_type, timestamp)
    WHERE event_type != '';
