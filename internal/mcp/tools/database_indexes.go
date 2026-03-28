package tools

import (
	"context"
	"fmt"
	"strings"


	"github.com/adham90/opentrace/internal/connector"
)

// ---------------------------------------------------------------------------
// Action: indexes — index analysis + bloat
// ---------------------------------------------------------------------------

func HandleIndexes(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	tableName := ArgString(args, "table_name")
	includeSuggestions := true
	if v, ok := args["include_suggestions"].(bool); ok {
		includeSuggestions = v
	}

	resp := map[string]any{}

	// --- Index analysis ---

	unused, err := fetchUnusedIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query unused indexes: %v", err)), nil
	}
	resp["unused_indexes"] = unused

	missing, err := fetchMissingIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query missing indexes: %v", err)), nil
	}
	resp["missing_indexes"] = missing

	duplicates, err := fetchDuplicateIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query duplicate indexes: %v", err)), nil
	}
	resp["duplicate_indexes"] = duplicates

	bloatedIdx, err := fetchBloatedIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query bloated indexes: %v", err)), nil
	}
	resp["bloated_indexes"] = bloatedIdx

	totalUnusedSize := int64(0)
	for _, u := range unused {
		if sz, ok := u["size_bytes"].(int64); ok {
			totalUnusedSize += sz
		}
	}

	// --- Bloat estimate ---

	bloatQuery := `SELECT
		schemaname || '.' || relname AS table_name,
		pg_size_pretty(pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname))) AS total_size,
		pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) AS total_bytes,
		n_live_tup,
		n_dead_tup,
		CASE WHEN n_live_tup + n_dead_tup > 0
			THEN ROUND(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 2)
			ELSE 0
		END AS dead_pct,
		CASE WHEN n_live_tup + n_dead_tup > 0 AND pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) > 0
			THEN pg_size_pretty(
				(pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) *
				 n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1))::bigint
			)
			ELSE '0 bytes'
		END AS estimated_bloat,
		CASE WHEN n_live_tup + n_dead_tup > 0 AND pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) > 0
			THEN (pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) *
				 n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1))::bigint
			ELSE 0
		END AS estimated_bloat_bytes,
		last_vacuum,
		last_autovacuum
	FROM pg_stat_user_tables
	WHERE n_dead_tup > 0 OR pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) > 1048576
	ORDER BY (pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) *
			  n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1)) DESC
	LIMIT 30`

	bloatResult, err := qe.ExecuteReadQuery(ctx, bloatQuery)
	var bloatTables []map[string]any
	var totalBloatBytes float64
	var bloatRecommendations []string

	if err == nil && bloatResult.RowCount > 0 {
		bloatTables = make([]map[string]any, 0, len(bloatResult.Rows))
		for _, row := range bloatResult.Rows {
			if len(row) < 10 {
				continue
			}
			t := map[string]any{
				"table":                 row[0],
				"total_size":            row[1],
				"total_bytes":           row[2],
				"live_tuples":           row[3],
				"dead_tuples":           row[4],
				"dead_pct":              row[5],
				"estimated_bloat":       row[6],
				"estimated_bloat_bytes": row[7],
				"last_vacuum":           row[8],
				"last_autovacuum":       row[9],
			}
			bloatTables = append(bloatTables, t)

			name := fmt.Sprintf("%v", row[0])
			deadPct, _ := toFloat64(row[5])
			bloatBytes, _ := toFloat64(row[7])
			totalBloatBytes += bloatBytes

			if deadPct > 30 {
				bloatRecommendations = append(bloatRecommendations, fmt.Sprintf("VACUUM FULL %s — %.0f%% dead tuples, would reclaim ~%v", name, deadPct, row[6]))
			} else if deadPct > 10 {
				bloatRecommendations = append(bloatRecommendations, fmt.Sprintf("VACUUM ANALYZE %s — %.0f%% dead tuples, estimated bloat: %v", name, deadPct, row[6]))
			}
		}
	}

	resp["summary"] = map[string]any{
		"unused":                  len(unused),
		"possibly_missing":        len(missing),
		"duplicates":              len(duplicates),
		"bloated_indexes":         len(bloatedIdx),
		"total_unused_size_human": humanSize(totalUnusedSize),
	}

	if len(bloatTables) > 0 {
		resp["bloat_estimate"] = map[string]any{
			"tables":                bloatTables,
			"total_estimated_bloat": fmt.Sprintf("%.0f bytes", totalBloatBytes),
			"recommendations":      bloatRecommendations,
		}
	}

	return JSONResult(resp)
}

func fetchUnusedIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		schemaname,
		relname AS table_name,
		indexrelname AS index_name,
		pg_relation_size(i.indexrelid) AS size_bytes,
		idx_scan
	FROM pg_stat_user_indexes i
	JOIN pg_index pi ON i.indexrelid = pi.indexrelid
	WHERE idx_scan = 0
	  AND NOT pi.indisunique
	  AND NOT pi.indisprimary`

	if tableName != "" {
		query += fmt.Sprintf(` AND relname = '%s'`, sanitizeIdentifier(tableName))
	}
	query += ` ORDER BY pg_relation_size(i.indexrelid) DESC`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 5 {
			continue
		}
		schema := toString(row[0])
		table := toString(row[1])
		indexName := toString(row[2])
		sizeBytes := toInt64(row[3])
		idxScans := toInt64(row[4])

		entry := map[string]any{
			"schema":      schema,
			"table":       table,
			"index_name":  indexName,
			"size_bytes":  sizeBytes,
			"size_human":  humanSize(sizeBytes),
			"index_scans": idxScans,
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("DROP INDEX CONCURRENTLY %s.%s; -- saves %s, 0 scans since stats reset", schema, indexName, humanSize(sizeBytes))
		}
		result = append(result, entry)
	}
	return result, nil
}

func fetchMissingIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		schemaname,
		relname AS table_name,
		seq_scan,
		seq_tup_read,
		idx_scan,
		n_live_tup
	FROM pg_stat_user_tables
	WHERE n_live_tup > 10000
	  AND seq_scan > COALESCE(idx_scan, 0) * 10`

	if tableName != "" {
		query += fmt.Sprintf(` AND relname = '%s'`, sanitizeIdentifier(tableName))
	}
	query += ` ORDER BY seq_scan DESC`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 6 {
			continue
		}
		schema := toString(row[0])
		table := toString(row[1])
		seqScan := toInt64(row[2])
		seqTupRead := toInt64(row[3])
		idxScan := toInt64(row[4])
		liveTup := toInt64(row[5])

		entry := map[string]any{
			"schema":       schema,
			"table":        table,
			"seq_scans":    seqScan,
			"seq_tup_read": seqTupRead,
			"idx_scans":    idxScan,
			"live_tuples":  liveTup,
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("Table '%s' has %d sequential scans vs %d index scans — likely missing an index on commonly filtered columns", table, seqScan, idxScan)
		}
		result = append(result, entry)
	}
	return result, nil
}

func fetchDuplicateIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		tablename,
		array_agg(indexname ORDER BY indexname) AS indexes,
		regexp_replace(indexdef, 'CREATE (UNIQUE )?INDEX .+ USING ', '') AS indexdef_normalized
	FROM (
		SELECT
			tablename,
			indexname,
			indexdef
		FROM pg_indexes
		WHERE schemaname = 'public'`

	if tableName != "" {
		query += fmt.Sprintf(` AND tablename = '%s'`, sanitizeIdentifier(tableName))
	}

	query += `
	) sub
	GROUP BY tablename, indexdef_normalized
	HAVING count(*) > 1
	ORDER BY tablename`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 2 {
			continue
		}
		table := toString(row[0])
		indexesRaw := toString(row[1])
		indexes := parsePostgresArray(indexesRaw)

		entry := map[string]any{
			"table":   table,
			"indexes": indexes,
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("Indexes %s cover the same definition — consider dropping one", strings.Join(indexes, " and "))
		}
		result = append(result, entry)
	}
	return result, nil
}

func fetchBloatedIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		s.schemaname,
		s.relname AS table_name,
		s.indexrelname AS index_name,
		pg_relation_size(s.relid) AS table_size,
		pg_relation_size(s.indexrelid) AS index_size
	FROM pg_stat_user_indexes s
	WHERE pg_relation_size(s.indexrelid) > pg_relation_size(s.relid)
	  AND pg_relation_size(s.relid) > 0`

	if tableName != "" {
		query += fmt.Sprintf(` AND s.relname = '%s'`, sanitizeIdentifier(tableName))
	}
	query += ` ORDER BY pg_relation_size(s.indexrelid) DESC`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 5 {
			continue
		}
		schema := toString(row[0])
		table := toString(row[1])
		indexName := toString(row[2])
		tableSize := toInt64(row[3])
		indexSize := toInt64(row[4])

		ratio := float64(indexSize) / float64(tableSize)
		entry := map[string]any{
			"schema":           schema,
			"table":            table,
			"index_name":       indexName,
			"table_size_bytes": tableSize,
			"index_size_bytes": indexSize,
			"ratio":            round2(ratio),
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("Index is %.1fx larger than table — consider REINDEX CONCURRENTLY", ratio)
		}
		result = append(result, entry)
	}
	return result, nil
}
