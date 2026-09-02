-- The endpoint/trend aggregators were disabled when log storage moved to the
-- segmented store: nothing has written these four tables since, and the MCP
-- actions that read them (analytics.*, code.annotate_*, code.deps_*) have been
-- removed. Dropping them stops the empty tables from being mistaken for a
-- working feature.
DROP TABLE IF EXISTS endpoint_stats;
DROP TABLE IF EXISTS traffic_heatmap;
DROP TABLE IF EXISTS metric_buckets;
DROP TABLE IF EXISTS deploy_markers;
