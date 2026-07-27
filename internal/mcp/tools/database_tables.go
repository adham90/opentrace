package tools

import (
	"context"
	"fmt"
)

// ---------------------------------------------------------------------------
// Action: tables — table stats
// ---------------------------------------------------------------------------

func HandleTables(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(ctx, deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	tableName := ArgString(args, "table_name")

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

	return JSONResult(resp)
}
