package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
)

// connectionPoolStatsHandler returns a handler that shows connection pool
// health: utilization, per-application breakdown, and warnings.
func connectionPoolStatsHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
		}
		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("The active database connector does not support direct queries."), nil
		}

		// Get max connections setting.
		maxConnResult, err := qe.ExecuteReadQuery(ctx, `SHOW max_connections`)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query max_connections: %v", err)), nil
		}
		maxConn := 100 // default
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to query connection stats: %v", err)), nil
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to query application stats: %v", err)), nil
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

		// Generate warnings.
		var warnings []string
		if utilPct > 80 {
			warnings = append(warnings, fmt.Sprintf("Pool utilization at %.0f%% — approaching saturation", utilPct))
		}
		if waiting > 0 {
			warnings = append(warnings, fmt.Sprintf("%d queries waiting for a connection", waiting))
		}
		if idleInTx > 5 {
			warnings = append(warnings, fmt.Sprintf("%d idle-in-transaction connections — these hold locks and waste pool slots", idleInTx))
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
		if len(warnings) > 0 {
			connectorInfo["warnings"] = warnings
		}

		resp := map[string]any{
			"connectors": []any{connectorInfo},
		}

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}
