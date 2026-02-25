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

// dbIndexAnalysisHandler returns a handler that analyzes index health:
// unused indexes, missing indexes, duplicate indexes, and bloated indexes.
func dbIndexAnalysisHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
		}
		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("The active database connector does not support direct queries."), nil
		}

		tableName, _ := args["table_name"].(string)
		includeSuggestions := true
		if v, ok := args["include_suggestions"].(bool); ok {
			includeSuggestions = v
		}

		resp := map[string]any{}

		unused, err := fetchUnusedIndexes(ctx, qe, tableName, includeSuggestions)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query unused indexes: %v", err)), nil
		}
		resp["unused_indexes"] = unused

		missing, err := fetchMissingIndexes(ctx, qe, tableName, includeSuggestions)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query missing indexes: %v", err)), nil
		}
		resp["missing_indexes"] = missing

		duplicates, err := fetchDuplicateIndexes(ctx, qe, tableName, includeSuggestions)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query duplicate indexes: %v", err)), nil
		}
		resp["duplicate_indexes"] = duplicates

		bloated, err := fetchBloatedIndexes(ctx, qe, tableName, includeSuggestions)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query bloated indexes: %v", err)), nil
		}
		resp["bloated_indexes"] = bloated

		totalUnusedSize := int64(0)
		for _, u := range unused {
			if sz, ok := u["size_bytes"].(int64); ok {
				totalUnusedSize += sz
			}
		}

		resp["summary"] = map[string]any{
			"unused":                  len(unused),
			"possibly_missing":        len(missing),
			"duplicates":              len(duplicates),
			"bloated":                 len(bloated),
			"total_unused_size_human": humanSize(totalUnusedSize),
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
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
		// Parse the array string from Postgres: {idx1,idx2}
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

// sanitizeIdentifier removes characters that aren't valid in a SQL identifier.
func sanitizeIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// parsePostgresArray parses a simple Postgres array literal like {a,b,c}.
func parsePostgresArray(s string) []string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

