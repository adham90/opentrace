package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// killQueryHandler returns a handler that cancels or terminates a backend
// process by PID using pg_cancel_backend() or pg_terminate_backend().
func killQueryHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		pidFloat, ok := args["pid"].(float64)
		if !ok || pidFloat <= 0 {
			return mcp.NewToolResultError("pid is required (positive integer). Use db_activity to find PIDs of long-running queries."), nil
		}
		pid := int(pidFloat)

		force := false
		if v, ok := args["force"].(bool); ok {
			force = v
		}

		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("The active database connector does not support direct queries."), nil
		}

		// First, check what the process is doing.
		infoQuery := fmt.Sprintf(`SELECT pid, state, COALESCE(application_name, '') AS app_name,
			LEFT(query, 300) AS query_preview,
			EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_seconds
		FROM pg_stat_activity WHERE pid = %d`, pid)

		infoResult, err := qe.ExecuteReadQuery(ctx, infoQuery)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to look up PID %d: %v", pid, err)), nil
		}

		if infoResult.RowCount == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("no active backend with PID %d found. The query may have already completed. Use db_activity to check current queries.", pid)), nil
		}

		// Extract process info for the response.
		var state, appName, queryPreview string
		var durationSec int64
		if len(infoResult.Rows) > 0 && len(infoResult.Rows[0]) >= 5 {
			row := infoResult.Rows[0]
			state = toString(row[1])
			appName = toString(row[2])
			queryPreview = toString(row[3])
			durationSec = toInt64(row[4])
		}

		// Cancel or terminate.
		var actionQuery string
		var action string
		if force {
			action = "terminated"
			actionQuery = fmt.Sprintf("SELECT pg_terminate_backend(%d)", pid)
		} else {
			action = "cancelled"
			actionQuery = fmt.Sprintf("SELECT pg_cancel_backend(%d)", pid)
		}

		result, err := qe.ExecuteReadQuery(ctx, actionQuery)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to %s PID %d: %v", action[:len(action)-2], pid, err)), nil
		}

		success := false
		if len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
			if v, ok := result.Rows[0][0].(bool); ok {
				success = v
			}
		}

		resp := map[string]any{
			"pid":              pid,
			"action":           action,
			"success":          success,
			"state_before":     state,
			"application_name": appName,
			"query_preview":    queryPreview,
			"duration_seconds": durationSec,
		}

		if !success {
			resp["note"] = fmt.Sprintf("pg_%s_backend returned false — the process may have already ended or you may lack permission. Use db_activity to verify.", action[:len(action)-1])
		}

		if !force && success {
			resp["hint"] = "Query was cancelled gracefully. If the process doesn't stop, use force=true to terminate it."
		}

		// Track killed PID on session
		if success && sessionTracker != nil {
			if sess := sessionTracker.CurrentSession(); sess != nil {
				killed := append([]string{}, sess.KilledQueries...)
				killed = append(killed, fmt.Sprintf("%d", pid))
				sessionTracker.UpdateSession(store.UpdateInvestigationSessionParams{
					KilledQueries: killed,
				})
			}
		}

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
