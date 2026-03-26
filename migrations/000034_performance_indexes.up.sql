-- Performance indexes for heavy aggregation queries

-- Metrics: LatestByServer() subquery (metric_store.go)
CREATE INDEX IF NOT EXISTS idx_metrics_server_name_ts
    ON metrics(server_id, metric_name, timestamp DESC);

-- Trends: AggregateBuckets() GROUP BY (trend_store.go)
CREATE INDEX IF NOT EXISTS idx_logs_ts_service_env
    ON logs(timestamp, service, environment);

-- Analytics: AggregateEndpointStats() GROUP BY (analytics_store.go)
CREATE INDEX IF NOT EXISTS idx_logs_ts_level
    ON logs(timestamp, level);

-- Journey: entry/exit path lookups (journey_store.go)
CREATE INDEX IF NOT EXISTS idx_logs_session_service_ts
    ON logs(session_id, service, timestamp)
    WHERE session_id != '';
