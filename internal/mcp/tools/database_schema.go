package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"


	"github.com/adham90/opentrace/internal/connector"
)

// ---------------------------------------------------------------------------
// Action: tables — table stats
// ---------------------------------------------------------------------------

func HandleTables(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	tableName, _ := args["table_name"].(string)

	query := `SELECT
  s.schemaname,
  s.relname AS table_name,
  s.n_live_tup,
  s.n_dead_tup,
  s.seq_scan,
  s.idx_scan,
  s.n_tup_ins,
  s.n_tup_upd,
  s.n_tup_del,
  s.last_vacuum,
  s.last_autovacuum,
  s.last_analyze,
  s.last_autoanalyze,
  COALESCE(io.heap_blks_hit, 0) AS heap_blks_hit,
  COALESCE(io.heap_blks_read, 0) AS heap_blks_read
FROM pg_stat_user_tables s
LEFT JOIN pg_statio_user_tables io
  ON s.relid = io.relid`

	if tableName != "" {
		query += fmt.Sprintf("\nWHERE s.relname = '%s'", sanitizeIdentifier(tableName))
	}

	query += "\nORDER BY s.n_live_tup DESC\nLIMIT 50"

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query table stats: %v", err)), nil
	}

	if result.RowCount == 0 {
		if tableName != "" {
			return NewToolResultText(fmt.Sprintf("No statistics found for table %q. Verify the table name exists.", tableName)), nil
		}
		return NewToolResultText("No user tables found in the database."), nil
	}

	type tableStats struct {
		Schema          string `json:"schema"`
		TableName       string `json:"table_name"`
		LiveTuples      any    `json:"live_tuples"`
		DeadTuples      any    `json:"dead_tuples"`
		SeqScans        any    `json:"seq_scans"`
		IndexScans      any    `json:"index_scans"`
		TupInserted     any    `json:"tuples_inserted"`
		TupUpdated      any    `json:"tuples_updated"`
		TupDeleted      any    `json:"tuples_deleted"`
		LastVacuum      any    `json:"last_vacuum"`
		LastAutovacuum  any    `json:"last_autovacuum"`
		LastAnalyze     any    `json:"last_analyze"`
		LastAutoanalyze any    `json:"last_autoanalyze"`
		HeapBlksHit     any    `json:"heap_blks_hit"`
		HeapBlksRead    any    `json:"heap_blks_read"`
	}

	tables := make([]tableStats, 0, len(result.Rows))
	var warnings []string

	for _, row := range result.Rows {
		if len(row) < 15 {
			continue
		}

		ts := tableStats{
			Schema:          fmt.Sprintf("%v", row[0]),
			TableName:       fmt.Sprintf("%v", row[1]),
			LiveTuples:      row[2],
			DeadTuples:      row[3],
			SeqScans:        row[4],
			IndexScans:      row[5],
			TupInserted:     row[6],
			TupUpdated:      row[7],
			TupDeleted:      row[8],
			LastVacuum:      row[9],
			LastAutovacuum:  row[10],
			LastAnalyze:     row[11],
			LastAutoanalyze: row[12],
			HeapBlksHit:     row[13],
			HeapBlksRead:    row[14],
		}
		tables = append(tables, ts)

		tblLabel := fmt.Sprintf("%s.%s", ts.Schema, ts.TableName)

		// Check dead tuple ratio.
		live := toInt64(row[2])
		dead := toInt64(row[3])
		if live > 0 && dead > 0 {
			ratio := float64(dead) / float64(live+dead) * 100
			if ratio > 10 {
				warnings = append(warnings, fmt.Sprintf("%s: %.0f%% dead tuples (%d dead / %d total) — consider VACUUM", tblLabel, ratio, dead, live+dead))
			}
		}

		// Check index usage.
		seqScans := toInt64(row[4])
		idxScans := toInt64(row[5])
		if seqScans+idxScans > 100 && live > 10000 {
			if idxScans == 0 || (seqScans > 0 && float64(idxScans)/float64(seqScans+idxScans)*100 < 50) {
				warnings = append(warnings, fmt.Sprintf("%s: low index usage — %d seq scans vs %d index scans on %d rows", tblLabel, seqScans, idxScans, live))
			}
		}

		// Check cache hit ratio.
		hit := toInt64(row[13])
		read := toInt64(row[14])
		if hit+read > 100 {
			cacheRatio := float64(hit) / float64(hit+read) * 100
			if cacheRatio < 90 {
				warnings = append(warnings, fmt.Sprintf("%s: low cache hit ratio (%.0f%%) — may need more shared_buffers", tblLabel, cacheRatio))
			}
		}

		// Check stale vacuum.
		if live > 10000 && row[9] == nil && row[10] == nil {
			warnings = append(warnings, fmt.Sprintf("%s: never vacuumed with %d live tuples", tblLabel, live))
		}
	}

	resp := map[string]any{
		"total_tables": len(tables),
		"tables":       tables,
		"warnings":     warnings,
		"hint":         "Tables with high dead tuple ratios, low index usage, or low cache hit ratios are good candidates for watchers.",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: activity — current connections/activity
// ---------------------------------------------------------------------------

func HandleDatabaseActivity(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	// 1. Connection summary by state.
	summaryQuery := `SELECT
  state,
  COALESCE(application_name, '') AS app_name,
  count(*) AS count
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
GROUP BY state, application_name
ORDER BY count DESC`

	summaryResult, err := qe.ExecuteReadQuery(ctx, summaryQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query connection summary: %v", err)), nil
	}

	type connSummary struct {
		State   string `json:"state"`
		AppName string `json:"app_name"`
		Count   any    `json:"count"`
	}
	summaries := make([]connSummary, 0, len(summaryResult.Rows))
	for _, row := range summaryResult.Rows {
		if len(row) < 3 {
			continue
		}
		summaries = append(summaries, connSummary{
			State:   fmt.Sprintf("%v", row[0]),
			AppName: fmt.Sprintf("%v", row[1]),
			Count:   row[2],
		})
	}

	// 2. Long-running queries (> 10 seconds).
	longRunningQuery := `SELECT
  pid,
  COALESCE(application_name, '') AS app_name,
  state,
  EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_seconds,
  LEFT(query, 200) AS query_preview
FROM pg_stat_activity
WHERE state = 'active'
  AND pid <> pg_backend_pid()
  AND query_start < now() - interval '10 seconds'
ORDER BY query_start ASC
LIMIT 20`

	longResult, err := qe.ExecuteReadQuery(ctx, longRunningQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query long-running queries: %v", err)), nil
	}

	type longQuery struct {
		PID             any    `json:"pid"`
		AppName         string `json:"app_name"`
		State           string `json:"state"`
		DurationSeconds any    `json:"duration_seconds"`
		QueryPreview    string `json:"query_preview"`
	}
	longQueries := make([]longQuery, 0, len(longResult.Rows))
	for _, row := range longResult.Rows {
		if len(row) < 5 {
			continue
		}
		longQueries = append(longQueries, longQuery{
			PID:             row[0],
			AppName:         fmt.Sprintf("%v", row[1]),
			State:           fmt.Sprintf("%v", row[2]),
			DurationSeconds: row[3],
			QueryPreview:    fmt.Sprintf("%v", row[4]),
		})
	}

	// 3. Idle-in-transaction sessions (> 1 minute).
	idleQuery := `SELECT
  pid,
  COALESCE(application_name, '') AS app_name,
  EXTRACT(EPOCH FROM (now() - state_change))::int AS idle_seconds,
  LEFT(query, 200) AS last_query
FROM pg_stat_activity
WHERE state = 'idle in transaction'
  AND pid <> pg_backend_pid()
  AND state_change < now() - interval '1 minute'
ORDER BY state_change ASC
LIMIT 20`

	idleResult, err := qe.ExecuteReadQuery(ctx, idleQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query idle-in-transaction sessions: %v", err)), nil
	}

	type idleSession struct {
		PID         any    `json:"pid"`
		AppName     string `json:"app_name"`
		IdleSeconds any    `json:"idle_seconds"`
		LastQuery   string `json:"last_query"`
	}
	idleSessions := make([]idleSession, 0, len(idleResult.Rows))
	for _, row := range idleResult.Rows {
		if len(row) < 4 {
			continue
		}
		idleSessions = append(idleSessions, idleSession{
			PID:         row[0],
			AppName:     fmt.Sprintf("%v", row[1]),
			IdleSeconds: row[2],
			LastQuery:   fmt.Sprintf("%v", row[3]),
		})
	}

	// 4. Max connections for utilization.
	maxConnResult, err := qe.ExecuteReadQuery(ctx, "SELECT current_setting('max_connections')")
	var maxConns int
	var totalConns int
	if err == nil && maxConnResult.RowCount > 0 && len(maxConnResult.Rows[0]) > 0 {
		maxConns, _ = strconv.Atoi(fmt.Sprintf("%v", maxConnResult.Rows[0][0]))
	}

	// Total current connections.
	totalResult, err := qe.ExecuteReadQuery(ctx, "SELECT count(*) FROM pg_stat_activity")
	if err == nil && totalResult.RowCount > 0 && len(totalResult.Rows[0]) > 0 {
		if v, vErr := strconv.Atoi(fmt.Sprintf("%v", totalResult.Rows[0][0])); vErr == nil {
			totalConns = v
		}
	}

	// Build warnings.
	var warnings []string
	if maxConns > 0 {
		utilization := float64(totalConns) / float64(maxConns) * 100
		if utilization > 80 {
			warnings = append(warnings, fmt.Sprintf("High connection utilization: %d/%d (%.0f%%)", totalConns, maxConns, utilization))
		}
	}
	if len(longQueries) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d long-running queries (>10s) detected", len(longQueries)))
	}
	if len(idleSessions) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d idle-in-transaction sessions (>1min) detected", len(idleSessions)))
	}

	resp := map[string]any{
		"total_connections":      totalConns,
		"connection_summary":     summaries,
		"long_running_queries":   longQueries,
		"idle_in_transaction":    idleSessions,
		"warnings":               warnings,
	}
	if maxConns > 0 {
		resp["max_connections"] = maxConns
		resp["utilization_percent"] = float64(totalConns) / float64(maxConns) * 100
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

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
	if v, ok := args["schema"].(string); ok && v != "" {
		schema = v
	}

	tableName, _ := args["table"].(string)

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

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
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

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal table detail: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
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

// ---------------------------------------------------------------------------
// Action: storage — disk usage + checkpoint stats + vacuum
// ---------------------------------------------------------------------------

func HandleStorage(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	resp := map[string]any{}

	// ---------- Disk usage ----------

	dbQuery := `SELECT
		current_database() AS database,
		pg_size_pretty(pg_database_size(current_database())) AS total_size,
		pg_database_size(current_database()) AS total_bytes`
	dbResult, err := qe.ExecuteReadQuery(ctx, dbQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("database size query failed: %v", err)), nil
	}

	tableQuery := `SELECT
		schemaname || '.' || relname AS table_name,
		pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size,
		pg_total_relation_size(c.oid) AS total_bytes,
		pg_size_pretty(pg_table_size(c.oid)) AS table_size,
		pg_table_size(c.oid) AS table_bytes,
		pg_size_pretty(pg_indexes_size(c.oid)) AS index_size,
		pg_indexes_size(c.oid) AS index_bytes,
		pg_size_pretty(COALESCE(pg_total_relation_size(c.reltoastrelid), 0)) AS toast_size,
		COALESCE(pg_total_relation_size(c.reltoastrelid), 0) AS toast_bytes
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
	ORDER BY pg_total_relation_size(c.oid) DESC
	LIMIT 30`

	tableResult, err := qe.ExecuteReadQuery(ctx, tableQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("table size query failed: %v", err)), nil
	}

	diskTables := make([]map[string]any, 0, len(tableResult.Rows))
	for _, row := range tableResult.Rows {
		if len(row) < 9 {
			continue
		}
		diskTables = append(diskTables, map[string]any{
			"table":       row[0],
			"total_size":  row[1],
			"total_bytes": row[2],
			"table_size":  row[3],
			"table_bytes": row[4],
			"index_size":  row[5],
			"index_bytes": row[6],
			"toast_size":  row[7],
			"toast_bytes": row[8],
		})
	}

	diskUsage := map[string]any{
		"tables":      diskTables,
		"table_count": len(diskTables),
	}

	if dbResult != nil && len(dbResult.Rows) > 0 && len(dbResult.Rows[0]) >= 3 {
		diskUsage["database"] = dbResult.Rows[0][0]
		diskUsage["database_size"] = dbResult.Rows[0][1]
		diskUsage["database_bytes"] = dbResult.Rows[0][2]
	}

	resp["disk_usage"] = diskUsage

	// ---------- Checkpoint stats ----------

	bgQuery := `SELECT
		checkpoints_timed,
		checkpoints_req,
		checkpoint_write_time,
		checkpoint_sync_time,
		buffers_checkpoint,
		buffers_clean,
		buffers_backend,
		maxwritten_clean,
		buffers_alloc,
		stats_reset
	FROM pg_stat_bgwriter`

	bgResult, err := qe.ExecuteReadQuery(ctx, bgQuery)
	if err == nil && len(bgResult.Rows) > 0 && len(bgResult.Rows[0]) >= 10 {
		row := bgResult.Rows[0]
		timedF, _ := toFloat64(row[0])
		reqF, _ := toFloat64(row[1])
		totalCkpt := timedF + reqF

		var reqPct float64
		if totalCkpt > 0 {
			reqPct = 100.0 * reqF / totalCkpt
		}

		var checkpointWarnings []string

		checkpoints := map[string]any{
			"checkpoints": map[string]any{
				"timed":          row[0],
				"requested":     row[1],
				"total":         totalCkpt,
				"requested_pct": fmt.Sprintf("%.1f%%", reqPct),
				"write_time_ms": row[2],
				"sync_time_ms":  row[3],
			},
			"buffers": map[string]any{
				"checkpoint":    row[4],
				"clean":         row[5],
				"backend":       row[6],
				"backend_fsync": row[7],
				"alloc":         row[8],
			},
			"stats_reset": row[9],
		}

		if reqPct > 50 {
			checkpointWarnings = append(checkpointWarnings, fmt.Sprintf("%.0f%% of checkpoints are requested (not timed) — increase max_wal_size or checkpoint_completion_target", reqPct))
		}

		backendF, _ := toFloat64(row[6])
		checkpointF, _ := toFloat64(row[4])
		if checkpointF > 0 && backendF/checkpointF > 0.1 {
			checkpointWarnings = append(checkpointWarnings, "High backend buffer writes — increase shared_buffers so the bgwriter handles more writes")
		}

		// Try pg_stat_wal for WAL generation rate (PG14+).
		walQuery := `SELECT wal_records, wal_fpi, wal_bytes,
			pg_size_pretty(wal_bytes::bigint) AS wal_size, stats_reset
		FROM pg_stat_wal`
		walResult, wErr := qe.ExecuteReadQuery(ctx, walQuery)
		if wErr == nil && len(walResult.Rows) > 0 && len(walResult.Rows[0]) >= 5 {
			wRow := walResult.Rows[0]
			checkpoints["wal"] = map[string]any{
				"records":          wRow[0],
				"full_page_images": wRow[1],
				"total_bytes":      wRow[2],
				"total_size":       wRow[3],
				"stats_reset":      wRow[4],
			}
		}

		// Current WAL position.
		walPosQuery := `SELECT pg_current_wal_lsn()::text, pg_walfile_name(pg_current_wal_lsn())`
		walPosResult, wErr := qe.ExecuteReadQuery(ctx, walPosQuery)
		if wErr == nil && len(walPosResult.Rows) > 0 && len(walPosResult.Rows[0]) >= 2 {
			checkpoints["wal_position"] = map[string]any{
				"lsn":  walPosResult.Rows[0][0],
				"file": walPosResult.Rows[0][1],
			}
		}

		checkpoints["warnings"] = checkpointWarnings
		resp["checkpoint_stats"] = checkpoints
	}

	// ---------- Vacuum report ----------

	vacQuery := `SELECT
		schemaname,
		relname AS table_name,
		n_live_tup,
		n_dead_tup,
		CASE WHEN n_live_tup + n_dead_tup > 0
			THEN ROUND(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 2)
			ELSE 0
		END AS dead_tuple_pct,
		last_vacuum,
		last_autovacuum,
		last_analyze,
		last_autoanalyze,
		vacuum_count,
		autovacuum_count,
		pg_size_pretty(pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname))) AS total_size,
		n_tup_ins,
		n_tup_upd,
		n_tup_del
	FROM pg_stat_user_tables
	ORDER BY n_dead_tup DESC
	LIMIT 50`

	vacResult, err := qe.ExecuteReadQuery(ctx, vacQuery)
	if err == nil && vacResult.RowCount > 0 {
		vacTables := make([]map[string]any, 0, len(vacResult.Rows))
		var vacRecommendations []string

		for _, row := range vacResult.Rows {
			if len(row) < 15 {
				continue
			}
			vt := map[string]any{
				"schema":           row[0],
				"table":            row[1],
				"live_tuples":      row[2],
				"dead_tuples":      row[3],
				"dead_tuple_pct":   row[4],
				"last_vacuum":      row[5],
				"last_autovacuum":  row[6],
				"last_analyze":     row[7],
				"last_autoanalyze": row[8],
				"vacuum_count":     row[9],
				"autovacuum_count": row[10],
				"total_size":       row[11],
				"inserts":          row[12],
				"updates":          row[13],
				"deletes":          row[14],
			}
			vacTables = append(vacTables, vt)

			vtName := fmt.Sprintf("%v.%v", row[0], row[1])
			if pct, ok := toFloat64(row[4]); ok && pct > 10 {
				vacRecommendations = append(vacRecommendations, fmt.Sprintf("VACUUM ANALYZE %s — %.1f%% dead tuples", vtName, pct))
			}
			if row[5] == nil && row[6] == nil {
				vacRecommendations = append(vacRecommendations, fmt.Sprintf("VACUUM %s — never vacuumed", vtName))
			}

			updates, _ := toFloat64(row[13])
			deletes, _ := toFloat64(row[14])
			if updates+deletes > 100000 && row[5] == nil && row[6] == nil {
				vacRecommendations = append(vacRecommendations, fmt.Sprintf("%s has %.0f updates + %.0f deletes but was never vacuumed", vtName, updates, deletes))
			}
		}

		// Fetch autovacuum settings.
		avQuery := `SELECT name, setting FROM pg_settings
			WHERE name IN ('autovacuum', 'autovacuum_vacuum_threshold', 'autovacuum_vacuum_scale_factor',
				'autovacuum_analyze_threshold', 'autovacuum_analyze_scale_factor', 'autovacuum_naptime',
				'autovacuum_max_workers')`
		avSettings := make(map[string]string)
		avResult, avErr := qe.ExecuteReadQuery(ctx, avQuery)
		if avErr == nil {
			for _, row := range avResult.Rows {
				if len(row) >= 2 {
					avSettings[fmt.Sprintf("%v", row[0])] = fmt.Sprintf("%v", row[1])
				}
			}
		}

		resp["vacuum_report"] = map[string]any{
			"tables":              vacTables,
			"total_tables":        len(vacTables),
			"recommendations":     vacRecommendations,
			"autovacuum_settings": avSettings,
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: connections — pool stats + replication
// ---------------------------------------------------------------------------

func HandleConnections(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	resp := map[string]any{}

	// ---------- Pool stats ----------

	// Get max connections setting.
	maxConnResult, err := qe.ExecuteReadQuery(ctx, `SELECT current_setting('max_connections')`)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query max_connections: %v", err)), nil
	}
	maxConn := 100
	if len(maxConnResult.Rows) > 0 && len(maxConnResult.Rows[0]) > 0 {
		if v, err := fmt.Sscanf(toString(maxConnResult.Rows[0][0]), "%d", &maxConn); v == 0 || err != nil {
			maxConn = 100
		}
	}

	// Get connection summary.
	summaryQuery := `SELECT
		count(*) AS total,
		count(*) FILTER (WHERE state = 'active') AS active,
		count(*) FILTER (WHERE state = 'idle') AS idle,
		count(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx,
		count(*) FILTER (WHERE wait_event_type = 'Lock') AS waiting
	FROM pg_stat_activity
	WHERE backend_type = 'client backend'`

	summaryResult, err := qe.ExecuteReadQuery(ctx, summaryQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query connection stats: %v", err)), nil
	}

	var total, active, idle, idleInTx, waiting int64
	if len(summaryResult.Rows) > 0 {
		row := summaryResult.Rows[0]
		if len(row) >= 5 {
			total = toInt64(row[0])
			active = toInt64(row[1])
			idle = toInt64(row[2])
			idleInTx = toInt64(row[3])
			waiting = toInt64(row[4])
		}
	}

	utilPct := float64(0)
	if maxConn > 0 {
		utilPct = float64(total) / float64(maxConn) * 100
	}

	// Get per-application breakdown.
	appQuery := `SELECT
		COALESCE(application_name, '') AS app,
		count(*) AS connections,
		count(*) FILTER (WHERE state = 'active') AS active,
		count(*) FILTER (WHERE state = 'idle') AS idle
	FROM pg_stat_activity
	WHERE backend_type = 'client backend'
	GROUP BY application_name
	ORDER BY count(*) DESC`

	appResult, err := qe.ExecuteReadQuery(ctx, appQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query application stats: %v", err)), nil
	}

	var byApp []map[string]any
	for _, row := range appResult.Rows {
		if len(row) < 4 {
			continue
		}
		byApp = append(byApp, map[string]any{
			"application_name": toString(row[0]),
			"connections":      toInt64(row[1]),
			"active":           toInt64(row[2]),
			"idle":             toInt64(row[3]),
		})
	}

	// Generate pool warnings.
	var poolWarnings []string
	if utilPct > 80 {
		poolWarnings = append(poolWarnings, fmt.Sprintf("Pool utilization at %.0f%% — approaching saturation", utilPct))
	}
	if waiting > 0 {
		poolWarnings = append(poolWarnings, fmt.Sprintf("%d queries waiting for a connection", waiting))
	}
	if idleInTx > 5 {
		poolWarnings = append(poolWarnings, fmt.Sprintf("%d idle-in-transaction connections — these hold locks and waste pool slots", idleInTx))
	}

	pool := map[string]any{
		"max_connections":     maxConn,
		"current_connections": total,
		"active_connections":  active,
		"idle_connections":    idle,
		"idle_in_transaction": idleInTx,
		"utilization_pct":     round2(utilPct),
		"waiting_queries":     waiting,
	}

	connectorInfo := map[string]any{
		"pool":           pool,
		"by_application": byApp,
	}
	if len(poolWarnings) > 0 {
		connectorInfo["warnings"] = poolWarnings
	}

	resp["pool_stats"] = connectorInfo

	// ---------- Replication status ----------

	var replWarnings []string

	// Check if primary or replica.
	roleResult, err := qe.ExecuteReadQuery(ctx, "SELECT pg_is_in_recovery()")
	if err != nil {
		// Non-fatal — just skip replication info.
		resp["replication"] = map[string]any{"error": fmt.Sprintf("failed to check server role: %v", err)}
		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}

	isReplica := false
	if len(roleResult.Rows) > 0 && len(roleResult.Rows[0]) > 0 {
		if v, ok := roleResult.Rows[0][0].(bool); ok {
			isReplica = v
		}
	}

	repl := map[string]any{}
	if isReplica {
		repl["role"] = "replica"
	} else {
		repl["role"] = "primary"
	}

	// Replication slots.
	slotQuery := `SELECT
		slot_name,
		slot_type,
		active,
		COALESCE(restart_lsn::text, '') AS restart_lsn,
		COALESCE(confirmed_flush_lsn::text, '') AS confirmed_flush_lsn,
		COALESCE(wal_status, 'unknown') AS wal_status
	FROM pg_replication_slots
	ORDER BY slot_name`

	slotResult, err := qe.ExecuteReadQuery(ctx, slotQuery)
	if err == nil && slotResult.RowCount > 0 {
		var slots []map[string]any
		for _, row := range slotResult.Rows {
			if len(row) < 6 {
				continue
			}
			slotActive := false
			if v, ok := row[2].(bool); ok {
				slotActive = v
			}
			slot := map[string]any{
				"slot_name":           toString(row[0]),
				"slot_type":           toString(row[1]),
				"active":              slotActive,
				"restart_lsn":         toString(row[3]),
				"confirmed_flush_lsn": toString(row[4]),
				"wal_status":          toString(row[5]),
			}
			slots = append(slots, slot)

			if !slotActive {
				replWarnings = append(replWarnings, fmt.Sprintf("Replication slot '%s' is inactive — this can cause WAL retention bloat", toString(row[0])))
			}
			walStatus := toString(row[5])
			if walStatus == "lost" {
				replWarnings = append(replWarnings, fmt.Sprintf("Replication slot '%s' has WAL status 'lost' — the replica using this slot will need to be re-initialized", toString(row[0])))
			}
		}
		repl["replication_slots"] = slots
	} else {
		repl["replication_slots"] = []any{}
	}

	// Connected replicas (on primary).
	statQuery := `SELECT
		client_addr,
		state,
		COALESCE(application_name, '') AS application_name,
		sent_lsn::text AS sent_lsn,
		write_lsn::text AS write_lsn,
		flush_lsn::text AS flush_lsn,
		replay_lsn::text AS replay_lsn,
		EXTRACT(EPOCH FROM write_lag)::int AS write_lag_seconds,
		EXTRACT(EPOCH FROM flush_lag)::int AS flush_lag_seconds,
		EXTRACT(EPOCH FROM replay_lag)::int AS replay_lag_seconds
	FROM pg_stat_replication
	ORDER BY client_addr`

	statResult, err := qe.ExecuteReadQuery(ctx, statQuery)
	if err == nil && statResult.RowCount > 0 {
		var replicas []map[string]any
		for _, row := range statResult.Rows {
			if len(row) < 10 {
				continue
			}
			replayLag := toInt64(row[9])
			replica := map[string]any{
				"client_addr":        toString(row[0]),
				"state":              toString(row[1]),
				"application_name":   toString(row[2]),
				"sent_lsn":           toString(row[3]),
				"write_lsn":          toString(row[4]),
				"flush_lsn":          toString(row[5]),
				"replay_lsn":         toString(row[6]),
				"write_lag_seconds":  toInt64(row[7]),
				"flush_lag_seconds":  toInt64(row[8]),
				"replay_lag_seconds": replayLag,
			}
			replicas = append(replicas, replica)

			if replayLag > 30 {
				replWarnings = append(replWarnings, fmt.Sprintf("Replica %s has replay lag of %ds — investigate if the replica is overloaded or network is slow", toString(row[0]), replayLag))
			}
		}
		repl["connected_replicas"] = replicas
	} else {
		repl["connected_replicas"] = []any{}
	}

	// If replica, show lag from primary.
	if isReplica {
		lagQuery := `SELECT
			CASE WHEN pg_last_wal_receive_lsn() IS NOT NULL AND pg_last_wal_replay_lsn() IS NOT NULL
				THEN pg_wal_lsn_diff(pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn())
				ELSE 0
			END AS replay_lag_bytes,
			COALESCE(pg_last_wal_receive_lsn()::text, 'unknown') AS last_received_lsn,
			COALESCE(pg_last_wal_replay_lsn()::text, 'unknown') AS last_replayed_lsn,
			CASE WHEN pg_last_xact_replay_timestamp() IS NOT NULL
				THEN EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))::int
				ELSE NULL
			END AS seconds_behind_primary`

		lagResult, err := qe.ExecuteReadQuery(ctx, lagQuery)
		if err == nil && len(lagResult.Rows) > 0 && len(lagResult.Rows[0]) >= 4 {
			row := lagResult.Rows[0]
			lagBytes := toInt64(row[0])
			secondsBehind := toInt64(row[3])

			repl["replica_status"] = map[string]any{
				"replay_lag_bytes":       lagBytes,
				"last_received_lsn":      toString(row[1]),
				"last_replayed_lsn":      toString(row[2]),
				"seconds_behind_primary": secondsBehind,
			}

			if secondsBehind > 60 {
				replWarnings = append(replWarnings, fmt.Sprintf("This replica is %d seconds behind the primary", secondsBehind))
			}
			if lagBytes > 100*1024*1024 {
				replWarnings = append(replWarnings, fmt.Sprintf("Replay lag is %d bytes — the replica may be struggling to keep up", lagBytes))
			}
		}
	}

	// WAL archival status.
	archiveQuery := `SELECT
		archived_count,
		last_archived_wal,
		last_archived_time::text,
		failed_count,
		COALESCE(last_failed_wal, '') AS last_failed_wal,
		COALESCE(last_failed_time::text, '') AS last_failed_time
	FROM pg_stat_archiver`

	archiveResult, err := qe.ExecuteReadQuery(ctx, archiveQuery)
	if err == nil && len(archiveResult.Rows) > 0 && len(archiveResult.Rows[0]) >= 6 {
		row := archiveResult.Rows[0]
		failedCount := toInt64(row[3])

		archive := map[string]any{
			"archived_count":     toInt64(row[0]),
			"last_archived_wal":  toString(row[1]),
			"last_archived_time": toString(row[2]),
			"failed_count":       failedCount,
		}
		if toString(row[4]) != "" {
			archive["last_failed_wal"] = toString(row[4])
			archive["last_failed_time"] = toString(row[5])
		}
		repl["wal_archival"] = archive

		if failedCount > 0 {
			replWarnings = append(replWarnings, fmt.Sprintf("WAL archiver has %d failed archives — check archive_command configuration and disk space", failedCount))
		}
	}

	if len(replWarnings) > 0 {
		repl["warnings"] = replWarnings
	}

	resp["replication"] = repl

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: kill_query — terminate a query (admin action)
// ---------------------------------------------------------------------------

func HandleKillQuery(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	pidFloat, ok := args["pid"].(float64)
	if !ok || pidFloat <= 0 {
		return NewToolResultError("pid is required (positive integer). Use database action=activity to find PIDs of long-running queries."), nil
	}
	pid := int(pidFloat)

	force := false
	if v, ok := args["force"].(bool); ok {
		force = v
	}

	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	// First, check what the process is doing.
	infoQuery := fmt.Sprintf(`SELECT pid, state, COALESCE(application_name, '') AS app_name,
		LEFT(query, 300) AS query_preview,
		EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_seconds
	FROM pg_stat_activity WHERE pid = %d`, pid)

	infoResult, err := qe.ExecuteReadQuery(ctx, infoQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to look up PID %d: %v", pid, err)), nil
	}

	if infoResult.RowCount == 0 {
		return NewToolResultError(fmt.Sprintf("no active backend with PID %d found. The query may have already completed. Use database action=activity to check current queries.", pid)), nil
	}

	var state, appName, queryPreview string
	var durationSec int64
	if len(infoResult.Rows) > 0 && len(infoResult.Rows[0]) >= 5 {
		row := infoResult.Rows[0]
		state = toString(row[1])
		appName = toString(row[2])
		queryPreview = toString(row[3])
		durationSec = toInt64(row[4])
	}

	// Cancel or terminate.
	var actionQuery string
	var action string
	if force {
		action = "terminated"
		actionQuery = fmt.Sprintf("SELECT pg_terminate_backend(%d)", pid)
	} else {
		action = "cancelled"
		actionQuery = fmt.Sprintf("SELECT pg_cancel_backend(%d)", pid)
	}

	result, err := qe.ExecuteReadQuery(ctx, actionQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to %s PID %d: %v", action[:len(action)-2], pid, err)), nil
	}

	success := false
	if len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
		if v, ok := result.Rows[0][0].(bool); ok {
			success = v
		}
	}

	resp := map[string]any{
		"pid":              pid,
		"action":           action,
		"success":          success,
		"state_before":     state,
		"application_name": appName,
		"query_preview":    queryPreview,
		"duration_seconds": durationSec,
	}

	if !success {
		resp["note"] = fmt.Sprintf("pg_%s_backend returned false — the process may have already ended or you may lack permission. Use database action=activity to verify.", action[:len(action)-1])
	}

	if !force && success {
		resp["hint"] = "Query was cancelled gracefully. If the process doesn't stop, use force=true to terminate it."
	}

	// Track killed PID on session.
	if success && deps.SessionTracking != nil {
		deps.SessionTracking.TrackKilledQuery(fmt.Sprintf("%d", pid))
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: long_transactions — find long-running sessions
// ---------------------------------------------------------------------------

func HandleLongTransactions(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	minDurationSec := 30.0
	if v, ok := args["min_duration_seconds"].(float64); ok && v > 0 {
		minDurationSec = v
	}

	query := fmt.Sprintf(`SELECT
		pid,
		usename,
		application_name,
		client_addr::text,
		state,
		EXTRACT(EPOCH FROM (now() - xact_start))::int AS xact_duration_sec,
		EXTRACT(EPOCH FROM (now() - state_change))::int AS state_duration_sec,
		EXTRACT(EPOCH FROM (now() - query_start))::int AS query_duration_sec,
		wait_event_type,
		wait_event,
		LEFT(query, 200) AS query_preview,
		xact_start,
		state_change
	FROM pg_stat_activity
	WHERE xact_start IS NOT NULL
		AND pid != pg_backend_pid()
		AND EXTRACT(EPOCH FROM (now() - xact_start)) > %v
	ORDER BY xact_start ASC`, minDurationSec)

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("long transactions query failed: %v", err)), nil
	}

	if result.RowCount == 0 {
		return NewToolResultText(fmt.Sprintf("No transactions running longer than %.0f seconds.", minDurationSec)), nil
	}

	transactions := make([]map[string]any, 0, len(result.Rows))
	var idleInTx int
	var warnings []string

	for _, row := range result.Rows {
		if len(row) < 13 {
			continue
		}
		state := fmt.Sprintf("%v", row[4])
		xactDur, _ := toFloat64(row[5])

		tx := map[string]any{
			"pid":                row[0],
			"user":               row[1],
			"application":        row[2],
			"client_addr":        row[3],
			"state":              state,
			"xact_duration_sec":  row[5],
			"state_duration_sec": row[6],
			"query_duration_sec": row[7],
			"wait_event_type":    row[8],
			"wait_event":         row[9],
			"query_preview":      row[10],
			"xact_start":         row[11],
			"state_change":       row[12],
		}
		transactions = append(transactions, tx)

		if state == "idle in transaction" || state == "idle in transaction (aborted)" {
			idleInTx++
			if xactDur > 300 {
				warnings = append(warnings, fmt.Sprintf("PID %v is idle in transaction for %.0f seconds — likely a leaked connection. Consider killing with database action=kill_query.", row[0], xactDur))
			}
		}
		if xactDur > 3600 {
			warnings = append(warnings, fmt.Sprintf("PID %v has a transaction open for %.0f seconds (>1hr) — prevents vacuum from cleaning dead tuples", row[0], xactDur))
		}
	}

	// Check what locks these transactions hold.
	lockQuery := `SELECT
		a.pid,
		l.locktype,
		l.mode,
		COALESCE(l.relation::regclass::text, '') AS relation,
		l.granted
	FROM pg_locks l
	JOIN pg_stat_activity a ON a.pid = l.pid
	WHERE a.xact_start IS NOT NULL
		AND a.pid != pg_backend_pid()
		AND l.granted = true
		AND l.locktype IN ('relation', 'tuple', 'advisory')
	ORDER BY a.pid`

	locks := make([]map[string]any, 0)
	lockResult, err := qe.ExecuteReadQuery(ctx, lockQuery)
	if err == nil {
		for _, row := range lockResult.Rows {
			if len(row) < 5 {
				continue
			}
			locks = append(locks, map[string]any{
				"pid":      row[0],
				"locktype": row[1],
				"mode":     row[2],
				"relation": row[3],
				"granted":  row[4],
			})
		}
	}

	resp := map[string]any{
		"transactions":        transactions,
		"total":               len(transactions),
		"idle_in_transaction": idleInTx,
		"locks_held":          locks,
		"warnings":            warnings,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}
