package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// pgConfigCheckHandler returns a handler that audits PostgreSQL configuration.
func pgConfigCheckHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		// Fetch key settings.
		query := `SELECT name, setting, unit, category, short_desc,
			CASE WHEN source = 'default' THEN 'default' ELSE source END AS source,
			boot_val, reset_val
		FROM pg_settings
		WHERE name IN (
			'shared_buffers', 'effective_cache_size', 'work_mem', 'maintenance_work_mem',
			'max_connections', 'max_worker_processes', 'max_parallel_workers',
			'max_parallel_workers_per_gather', 'wal_buffers', 'checkpoint_completion_target',
			'random_page_cost', 'effective_io_concurrency', 'max_wal_size', 'min_wal_size',
			'wal_level', 'max_replication_slots', 'hot_standby', 'archive_mode',
			'log_min_duration_statement', 'log_statement', 'log_lock_waits',
			'deadlock_timeout', 'lock_timeout', 'statement_timeout',
			'autovacuum', 'autovacuum_max_workers', 'autovacuum_naptime',
			'autovacuum_vacuum_threshold', 'autovacuum_vacuum_scale_factor',
			'autovacuum_analyze_threshold', 'autovacuum_analyze_scale_factor',
			'track_activity_query_size', 'default_statistics_target',
			'shared_preload_libraries', 'jit'
		)
		ORDER BY category, name`

		result, err := qe.ExecuteReadQuery(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("pg_settings query failed: %v", err)), nil
		}

		// Also fetch server info.
		infoQuery := `SELECT version(), pg_postmaster_start_time(),
			pg_size_pretty(pg_database_size(current_database())),
			current_database()`
		infoResult, _ := qe.ExecuteReadQuery(ctx, infoQuery)

		settings := make([]map[string]any, 0, len(result.Rows))
		var warnings []string

		for _, row := range result.Rows {
			if len(row) < 8 {
				continue
			}
			name := fmt.Sprintf("%v", row[0])
			setting := fmt.Sprintf("%v", row[1])
			unit := ""
			if row[2] != nil {
				unit = fmt.Sprintf("%v", row[2])
			}
			source := fmt.Sprintf("%v", row[5])

			s := map[string]any{
				"name":    name,
				"value":   setting,
				"source":  source,
			}
			if unit != "" {
				s["unit"] = unit
			}
			if row[4] != nil {
				s["description"] = row[4]
			}
			settings = append(settings, s)

			// Generate warnings for common misconfigurations.
			warnings = append(warnings, checkConfigWarning(name, setting, unit)...)
		}

		resp := map[string]any{
			"settings":     settings,
			"total":        len(settings),
			"warnings":     warnings,
		}

		// Add server info.
		if infoResult != nil && len(infoResult.Rows) > 0 && len(infoResult.Rows[0]) >= 4 {
			row := infoResult.Rows[0]
			resp["server_info"] = map[string]any{
				"version":    row[0],
				"started_at": row[1],
				"db_size":    row[2],
				"database":   row[3],
			}
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal config: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// checkConfigWarning returns warnings for known misconfiguration patterns.
func checkConfigWarning(name, setting, unit string) []string {
	var w []string
	val, _ := toFloat64(setting)

	switch name {
	case "shared_buffers":
		if unit == "8kB" && val < 16384 { // < 128MB
			w = append(w, "shared_buffers is below 128MB — recommended to set to 25% of total RAM")
		}
	case "work_mem":
		if unit == "kB" && val <= 4096 { // <= 4MB
			w = append(w, "work_mem is at default 4MB — consider increasing for complex queries (8-64MB typical)")
		}
	case "maintenance_work_mem":
		if unit == "kB" && val <= 65536 { // <= 64MB
			w = append(w, "maintenance_work_mem is low — increase to 256MB-1GB for faster VACUUM and index builds")
		}
	case "effective_cache_size":
		if unit == "8kB" && val < 65536 { // < 512MB
			w = append(w, "effective_cache_size is low — set to ~75% of total RAM for better query planning")
		}
	case "random_page_cost":
		if val >= 4 {
			w = append(w, "random_page_cost=4 is high for SSD storage — set to 1.1-1.5 for SSDs")
		}
	case "max_connections":
		if val > 200 {
			w = append(w, fmt.Sprintf("max_connections=%d is high — consider using a connection pooler (PgBouncer)", int(val)))
		}
	case "log_min_duration_statement":
		if setting == "-1" {
			w = append(w, "log_min_duration_statement is disabled — enable (e.g. 1000ms) to log slow queries")
		}
	case "autovacuum":
		if setting == "off" {
			w = append(w, "CRITICAL: autovacuum is disabled — this will cause table bloat and transaction ID wraparound")
		}
	case "log_lock_waits":
		if setting == "off" {
			w = append(w, "log_lock_waits is off — enable to detect lock contention in logs")
		}
	case "track_activity_query_size":
		if val < 4096 {
			w = append(w, "track_activity_query_size is small — increase to 4096+ to see full queries in pg_stat_activity")
		}
	case "default_statistics_target":
		if val <= 100 {
			w = append(w, "default_statistics_target=100 is default — increase to 200-500 for tables with skewed data distributions")
		}
	case "shared_preload_libraries":
		if !strings.Contains(setting, "pg_stat_statements") {
			w = append(w, "pg_stat_statements not in shared_preload_libraries — enable for query performance monitoring")
		}
	}
	return w
}
