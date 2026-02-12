package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// bloatEstimateHandler returns a handler that estimates table bloat using statistical methods.
func bloatEstimateHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active."), nil
		}
		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		// Statistical bloat estimation query (doesn't require pgstattuple extension).
		// Based on the widely-used bloat estimation approach from PostgreSQL wiki.
		query := `SELECT
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

		result, err := qe.ExecuteReadQuery(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("bloat estimation query failed: %v", err)), nil
		}

		if result.RowCount == 0 {
			return mcp.NewToolResultText("No tables with significant bloat found."), nil
		}

		tables := make([]map[string]any, 0, len(result.Rows))
		var totalBloatBytes float64
		var recommendations []string

		for _, row := range result.Rows {
			if len(row) < 10 {
				continue
			}
			t := map[string]any{
				"table":                row[0],
				"total_size":           row[1],
				"total_bytes":          row[2],
				"live_tuples":          row[3],
				"dead_tuples":          row[4],
				"dead_pct":             row[5],
				"estimated_bloat":      row[6],
				"estimated_bloat_bytes": row[7],
				"last_vacuum":          row[8],
				"last_autovacuum":      row[9],
			}
			tables = append(tables, t)

			name := fmt.Sprintf("%v", row[0])
			deadPct, _ := toFloat64(row[5])
			bloatBytes, _ := toFloat64(row[7])
			totalBloatBytes += bloatBytes

			if deadPct > 30 {
				recommendations = append(recommendations, fmt.Sprintf("VACUUM FULL %s — %.0f%% dead tuples, would reclaim ~%v", name, deadPct, row[6]))
			} else if deadPct > 10 {
				recommendations = append(recommendations, fmt.Sprintf("VACUUM ANALYZE %s — %.0f%% dead tuples, estimated bloat: %v", name, deadPct, row[6]))
			}
		}

		resp := map[string]any{
			"tables":               tables,
			"total_tables":         len(tables),
			"total_estimated_bloat": fmt.Sprintf("%.0f bytes", totalBloatBytes),
			"recommendations":      recommendations,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
