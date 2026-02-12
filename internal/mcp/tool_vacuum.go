package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// vacuumReportHandler returns a handler that generates a vacuum/maintenance report.
func vacuumReportHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Use test_connector to connect one first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		query := `SELECT
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
			pg_size_pretty(pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname))) AS total_size
		FROM pg_stat_user_tables
		ORDER BY n_dead_tup DESC
		LIMIT 50`

		result, err := qe.ExecuteReadQuery(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("vacuum report query failed: %v", err)), nil
		}

		if result.RowCount == 0 {
			return mcp.NewToolResultText("No user tables found."), nil
		}

		// Build structured report.
		tables := make([]map[string]any, 0, len(result.Rows))
		var recommendations []string

		for _, row := range result.Rows {
			if len(row) < 12 {
				continue
			}
			table := map[string]any{
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
			}
			tables = append(tables, table)

			// Check for tables needing maintenance.
			tableName := fmt.Sprintf("%v.%v", row[0], row[1])
			if pct, ok := toFloat64(row[4]); ok && pct > 10 {
				recommendations = append(recommendations, fmt.Sprintf("VACUUM ANALYZE %s — %.1f%% dead tuples", tableName, pct))
			}
			if row[5] == nil && row[6] == nil {
				recommendations = append(recommendations, fmt.Sprintf("VACUUM %s — never vacuumed", tableName))
			}
		}

		resp := map[string]any{
			"tables":          tables,
			"total_tables":    len(tables),
			"recommendations": recommendations,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal report: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// toFloat64 attempts to convert an interface value to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		var f float64
		_, err := fmt.Sscanf(n, "%f", &f)
		return f, err == nil
	default:
		return 0, false
	}
}

// vacuumRecommendation summarises a maintenance recommendation.
// Kept here for potential future use by the recommendations engine.
func vacuumRecommendation(tableName string, deadPct float64) string {
	var severity string
	switch {
	case deadPct > 30:
		severity = "HIGH"
	case deadPct > 10:
		severity = "MEDIUM"
	default:
		severity = "LOW"
	}
	return fmt.Sprintf("[%s] %s: %.1f%% dead tuples", severity, tableName, deadPct)
}

