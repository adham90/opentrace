package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/adham90/opentrace/internal/connector"
)

// ---------------------------------------------------------------------------
// Action: schema — schema overview + config check + sequences
// ---------------------------------------------------------------------------

func HandleSchema(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	resp := map[string]any{}

	// ---------- Schema overview ----------
	schema := "public"
	if v := ArgString(args, "schema"); v != "" {
		schema = v
	}

	tableName := ArgString(args, "table")

	if tableName != "" {
		schemaResult, err := schemaTableDetail(ctx, qe, schema, tableName)
		if err != nil {
			return nil, err
		}
		// For specific table detail, return directly.
		return schemaResult, nil
	}

	// All tables overview.
	tableQuery := fmt.Sprintf(`SELECT
		t.table_name,
		(SELECT count(*) FROM information_schema.columns c WHERE c.table_schema = t.table_schema AND c.table_name = t.table_name) AS column_count,
		pg_size_pretty(pg_total_relation_size(quote_ident(t.table_schema) || '.' || quote_ident(t.table_name))) AS size,
		obj_description((quote_ident(t.table_schema) || '.' || quote_ident(t.table_name))::regclass) AS comment,
		s.n_live_tup AS estimated_rows
	FROM information_schema.tables t
	LEFT JOIN pg_stat_user_tables s ON s.schemaname = t.table_schema AND s.relname = t.table_name
	WHERE t.table_schema = '%s' AND t.table_type = 'BASE TABLE'
	ORDER BY t.table_name`, schema)

	tableResult, err := qe.ExecuteReadQuery(ctx, tableQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("schema query failed: %v", err)), nil
	}

	if tableResult.RowCount == 0 {
		return NewToolResultText(fmt.Sprintf("No tables found in schema %q.", schema)), nil
	}

	tables := make([]map[string]any, 0, len(tableResult.Rows))
	for _, row := range tableResult.Rows {
		if len(row) < 5 {
			continue
		}
		t := map[string]any{
			"name":           row[0],
			"column_count":   row[1],
			"size":           row[2],
			"estimated_rows": row[4],
		}
		if row[3] != nil {
			t["comment"] = row[3]
		}
		tables = append(tables, t)
	}

	schemaOverview := map[string]any{
		"schema":      schema,
		"table_count": len(tables),
		"tables":      tables,
	}

	// Foreign key dependencies.
	depQuery := fmt.Sprintf(`SELECT
		tc.table_name AS from_table,
		ccu.table_name AS to_table,
		kcu.column_name AS from_column,
		ccu.column_name AS to_column
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
		ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
	JOIN information_schema.constraint_column_usage ccu
		ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
	WHERE tc.table_schema = '%s' AND tc.constraint_type = 'FOREIGN KEY'
	ORDER BY tc.table_name`, schema)

	depResult, err := qe.ExecuteReadQuery(ctx, depQuery)
	if err == nil && depResult.RowCount > 0 {
		fkDeps := make([]map[string]any, 0, len(depResult.Rows))
		for _, row := range depResult.Rows {
			if len(row) < 4 {
				continue
			}
			fkDeps = append(fkDeps, map[string]any{
				"from_table":  row[0],
				"to_table":    row[1],
				"from_column": row[2],
				"to_column":   row[3],
			})
		}
		schemaOverview["dependencies"] = fkDeps
	}

	resp["schema_overview"] = schemaOverview

	// ---------- Config check ----------

	configQuery := `SELECT name, setting, unit, category, short_desc,
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

	configResult, err := qe.ExecuteReadQuery(ctx, configQuery)
	if err == nil {
		infoQuery := `SELECT version(), pg_postmaster_start_time(),
			pg_size_pretty(pg_database_size(current_database())),
			current_database()`
		infoResult, _ := qe.ExecuteReadQuery(ctx, infoQuery)

		settings := make([]map[string]any, 0, len(configResult.Rows))
		var configWarnings []string

		for _, row := range configResult.Rows {
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
				"name":   name,
				"value":  setting,
				"source": source,
			}
			if unit != "" {
				s["unit"] = unit
			}
			if row[4] != nil {
				s["description"] = row[4]
			}
			settings = append(settings, s)

			configWarnings = append(configWarnings, checkConfigWarning(name, setting, unit)...)
		}

		configSection := map[string]any{
			"settings": settings,
			"total":    len(settings),
			"warnings": configWarnings,
		}

		if infoResult != nil && len(infoResult.Rows) > 0 && len(infoResult.Rows[0]) >= 4 {
			row := infoResult.Rows[0]
			configSection["server_info"] = map[string]any{
				"version":    row[0],
				"started_at": row[1],
				"db_size":    row[2],
				"database":   row[3],
			}
		}

		resp["config"] = configSection
	}

	// ---------- Sequence health ----------

	seqQuery := `SELECT
		schemaname || '.' || sequencename AS sequence_name,
		data_type,
		last_value,
		start_value,
		min_value,
		max_value,
		increment_by,
		cycle,
		CASE
			WHEN max_value > 0 AND last_value IS NOT NULL THEN
				ROUND(100.0 * (last_value - min_value) / NULLIF(max_value - min_value, 0), 2)
			ELSE 0
		END AS usage_pct
	FROM pg_sequences
	ORDER BY usage_pct DESC NULLS LAST`

	seqResult, err := qe.ExecuteReadQuery(ctx, seqQuery)
	if err == nil && seqResult.RowCount > 0 {
		sequences := make([]map[string]any, 0, len(seqResult.Rows))
		var seqWarnings []string

		for _, row := range seqResult.Rows {
			if len(row) < 9 {
				continue
			}
			name := fmt.Sprintf("%v", row[0])
			usagePct, _ := toFloat64(row[8])

			seq := map[string]any{
				"name":         name,
				"data_type":    row[1],
				"last_value":   row[2],
				"start_value":  row[3],
				"min_value":    row[4],
				"max_value":    row[5],
				"increment_by": row[6],
				"cycle":        row[7],
				"usage_pct":    usagePct,
			}
			sequences = append(sequences, seq)

			if usagePct > 75 {
				seqWarnings = append(seqWarnings, fmt.Sprintf("CRITICAL: %s is at %.1f%% capacity — will exhaust soon", name, usagePct))
			} else if usagePct > 50 {
				seqWarnings = append(seqWarnings, fmt.Sprintf("WARNING: %s is at %.1f%% capacity — monitor closely", name, usagePct))
			}

			if fmt.Sprintf("%v", row[1]) == "integer" && usagePct > 25 {
				seqWarnings = append(seqWarnings, fmt.Sprintf("%s uses integer type (max 2.1B) at %.1f%% — consider migrating to bigint", name, usagePct))
			}
		}

		resp["sequences"] = map[string]any{
			"sequences": sequences,
			"total":     len(sequences),
			"warnings":  seqWarnings,
		}
	}

	return JSONResult(resp)
}

// schemaTableDetail returns detailed information for a specific table.
func schemaTableDetail(ctx context.Context, qe connector.QueryExecutor, schema, tableName string) (*CallToolResult, error) {
	// Columns query.
	colQuery := fmt.Sprintf(`SELECT
		column_name,
		data_type,
		is_nullable,
		column_default,
		character_maximum_length
	FROM information_schema.columns
	WHERE table_schema = '%s' AND table_name = '%s'
	ORDER BY ordinal_position`, schema, tableName)

	colResult, err := qe.ExecuteReadQuery(ctx, colQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("columns query failed: %v", err)), nil
	}

	if colResult.RowCount == 0 {
		return NewToolResultError(fmt.Sprintf("table %q not found in schema %q", tableName, schema)), nil
	}

	columns := make([]map[string]any, 0, len(colResult.Rows))
	for _, row := range colResult.Rows {
		if len(row) < 5 {
			continue
		}
		col := map[string]any{
			"name":     row[0],
			"type":     row[1],
			"nullable": row[2],
		}
		if row[3] != nil {
			col["default"] = row[3]
		}
		if row[4] != nil {
			col["max_length"] = row[4]
		}
		columns = append(columns, col)
	}

	// Indexes query.
	idxQuery := fmt.Sprintf(`SELECT
		indexname,
		indexdef
	FROM pg_indexes
	WHERE schemaname = '%s' AND tablename = '%s'
	ORDER BY indexname`, schema, tableName)

	indexes := make([]map[string]any, 0)
	idxResult, err := qe.ExecuteReadQuery(ctx, idxQuery)
	if err == nil {
		for _, row := range idxResult.Rows {
			if len(row) < 2 {
				continue
			}
			indexes = append(indexes, map[string]any{
				"name":       row[0],
				"definition": row[1],
			})
		}
	}

	// Foreign keys query.
	fkQuery := fmt.Sprintf(`SELECT
		tc.constraint_name,
		kcu.column_name,
		ccu.table_name AS foreign_table,
		ccu.column_name AS foreign_column
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
		ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
	JOIN information_schema.constraint_column_usage ccu
		ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
	WHERE tc.table_schema = '%s' AND tc.table_name = '%s' AND tc.constraint_type = 'FOREIGN KEY'`, schema, tableName)

	foreignKeys := make([]map[string]any, 0)
	fkResult, err := qe.ExecuteReadQuery(ctx, fkQuery)
	if err == nil {
		for _, row := range fkResult.Rows {
			if len(row) < 4 {
				continue
			}
			foreignKeys = append(foreignKeys, map[string]any{
				"constraint":     row[0],
				"column":         row[1],
				"foreign_table":  row[2],
				"foreign_column": row[3],
			})
		}
	}

	resp := map[string]any{
		"schema":       schema,
		"table":        tableName,
		"columns":      columns,
		"column_count": len(columns),
		"indexes":      indexes,
		"foreign_keys": foreignKeys,
	}

	return JSONResult(resp)
}

// checkConfigWarning returns warnings for known misconfiguration patterns.
func checkConfigWarning(name, setting, unit string) []string {
	var w []string
	val, _ := toFloat64(setting)

	switch name {
	case "shared_buffers":
		if unit == "8kB" && val < 16384 {
			w = append(w, "shared_buffers is below 128MB — recommended to set to 25% of total RAM")
		}
	case "work_mem":
		if unit == "kB" && val <= 4096 {
			w = append(w, "work_mem is at default 4MB — consider increasing for complex queries (8-64MB typical)")
		}
	case "maintenance_work_mem":
		if unit == "kB" && val <= 65536 {
			w = append(w, "maintenance_work_mem is low — increase to 256MB-1GB for faster VACUUM and index builds")
		}
	case "effective_cache_size":
		if unit == "8kB" && val < 65536 {
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
