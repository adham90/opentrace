package tools

import (
	"context"
	"fmt"
)

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

	return JSONResult(resp)
}
