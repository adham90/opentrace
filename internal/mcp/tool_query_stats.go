package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// queryStatsHandler returns a handler that queries pg_stat_statements for
// the top SQL queries by the requested metric (calls, total_exec_time, etc.).
func queryStatsHandler(registry *connector.Registry, ws store.WatcherStore) server.ToolHandlerFunc {
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

		orderBy := "total_exec_time"
		if v, ok := args["order_by"].(string); ok && v != "" {
			allowed := map[string]bool{
				"calls": true, "total_exec_time": true, "mean_exec_time": true,
				"rows": true, "shared_blks_hit": true, "shared_blks_read": true,
			}
			if allowed[v] {
				orderBy = v
			}
		}

		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
			if limit > 100 {
				limit = 100
			}
		}

		query := fmt.Sprintf(`SELECT
  queryid,
  LEFT(query, 200) AS query_preview,
  calls,
  total_exec_time,
  mean_exec_time,
  rows,
  shared_blks_hit,
  shared_blks_read
FROM pg_stat_statements
ORDER BY %s DESC
LIMIT %d`, orderBy, limit)

		result, err := qe.ExecuteReadQuery(ctx, query)
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "pg_stat_statements") && strings.Contains(errMsg, "does not exist") {
				return mcp.NewToolResultText(
					"pg_stat_statements extension is not enabled.\n\n" +
						"To enable it:\n" +
						"1. Add to postgresql.conf: shared_preload_libraries = 'pg_stat_statements'\n" +
						"2. Restart PostgreSQL\n" +
						"3. Run: CREATE EXTENSION IF NOT EXISTS pg_stat_statements;\n\n" +
						"This extension tracks query execution statistics and is very useful for identifying slow queries."), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to query pg_stat_statements: %v", err)), nil
		}

		if result.RowCount == 0 {
			return mcp.NewToolResultText("No query statistics found. The database may have been recently restarted, or pg_stat_statements may have no recorded queries yet."), nil
		}

		// Build structured response.
		type queryStatsRow struct {
			QueryID        any     `json:"query_id"`
			QueryPreview   string  `json:"query_preview"`
			Calls          any     `json:"calls"`
			TotalExecTime  any     `json:"total_exec_time_ms"`
			MeanExecTime   any     `json:"mean_exec_time_ms"`
			Rows           any     `json:"rows"`
			SharedBlksHit  any     `json:"shared_blks_hit"`
			SharedBlksRead any     `json:"shared_blks_read"`
		}

		rows := make([]queryStatsRow, 0, len(result.Rows))
		for _, row := range result.Rows {
			if len(row) < 8 {
				continue
			}
			rows = append(rows, queryStatsRow{
				QueryID:        row[0],
				QueryPreview:   fmt.Sprintf("%v", row[1]),
				Calls:          row[2],
				TotalExecTime:  row[3],
				MeanExecTime:   row[4],
				Rows:           row[5],
				SharedBlksHit:  row[6],
				SharedBlksRead: row[7],
			})
		}

		resp := map[string]any{
			"order_by":    orderBy,
			"limit":       limit,
			"total_found": len(rows),
			"queries":     rows,
			"hint":        "Use these stats to identify slow queries, high-frequency queries, or queries with poor cache hit ratios. Consider creating watchers for the most impactful ones.",
		}

		appendExistingWatchers(resp, fetchExistingWatchers(ctx, ws))

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
