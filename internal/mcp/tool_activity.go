package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// dbActivityHandler returns a handler that queries pg_stat_activity to show
// current database activity: connection summary, long-running queries, and
// idle-in-transaction sessions.
func dbActivityHandler(registry *connector.Registry, ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("The active database connector does not support direct queries."), nil
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to query connection summary: %v", err)), nil
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to query long-running queries: %v", err)), nil
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to query idle-in-transaction sessions: %v", err)), nil
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
		maxConnResult, err := qe.ExecuteReadQuery(ctx, "SHOW max_connections")
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
			"total_connections": totalConns,
			"connection_summary": summaries,
			"long_running_queries": longQueries,
			"idle_in_transaction":  idleSessions,
			"warnings":             warnings,
		}
		if maxConns > 0 {
			resp["max_connections"] = maxConns
			resp["utilization_percent"] = float64(totalConns) / float64(maxConns) * 100
		}

		appendExistingMonitors(resp, fetchExistingMonitors(ctx, ws))

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
