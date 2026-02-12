package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// diskUsageHandler returns a handler that shows detailed disk usage breakdown.
func diskUsageHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active."), nil
		}
		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		// Database-level summary.
		dbQuery := `SELECT
			current_database() AS database,
			pg_size_pretty(pg_database_size(current_database())) AS total_size,
			pg_database_size(current_database()) AS total_bytes`
		dbResult, err := qe.ExecuteReadQuery(ctx, dbQuery)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("database size query failed: %v", err)), nil
		}

		// Per-table breakdown.
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
			return mcp.NewToolResultError(fmt.Sprintf("table size query failed: %v", err)), nil
		}

		tables := make([]map[string]any, 0, len(tableResult.Rows))
		for _, row := range tableResult.Rows {
			if len(row) < 9 {
				continue
			}
			tables = append(tables, map[string]any{
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

		resp := map[string]any{
			"tables":      tables,
			"table_count": len(tables),
		}

		if dbResult != nil && len(dbResult.Rows) > 0 && len(dbResult.Rows[0]) >= 3 {
			resp["database"] = dbResult.Rows[0][0]
			resp["database_size"] = dbResult.Rows[0][1]
			resp["database_bytes"] = dbResult.Rows[0][2]
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
