package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// stopWatcherRunTool returns the tool definition for stop_watcher_run.
func stopWatcherRunTool() mcp.Tool {
	return mcp.NewTool("stop_watcher_run",
		mcp.WithDescription("Stop a currently running watcher execution. Cancels the run's context so the agent loop or rule evaluation stops. Use when a watcher run is taking too long or you want to abort it."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run UUID (from watcher_run_history or get_run_details)")),
	)
}

// stopWatcherRunHandler returns a handler that cancels an active watcher run.
func stopWatcherRunHandler(hub *watcher.EventHub) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		runIDStr, _ := args["run_id"].(string)
		if runIDStr == "" {
			return mcp.NewToolResultError("run_id is required. Use watcher_run_history to find active run IDs."), nil
		}

		runID, err := uuid.Parse(runIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid run_id format — expected a UUID."), nil
		}

		cancelled := hub.Cancel(runID)
		if !cancelled {
			return mcp.NewToolResultError(fmt.Sprintf("Run %s not found or already completed. Only actively running watchers can be stopped.", runIDStr)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Run %s has been cancelled. The watcher execution will stop shortly.", runIDStr)), nil
	}
}

// deleteServerTool returns the tool definition for delete_server.
func deleteServerTool() mcp.Tool {
	return mcp.NewTool("delete_server",
		mcp.WithDescription("Delete a monitored server and its associated metrics. Use when a server has been decommissioned or is no longer monitored."),
		mcp.WithString("server_id", mcp.Required(), mcp.Description("Server UUID (from list_servers)")),
	)
}

// deleteServerHandler returns a handler that deletes a server.
func deleteServerHandler(ss store.ServerStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		serverIDStr, _ := args["server_id"].(string)
		if serverIDStr == "" {
			return mcp.NewToolResultError("server_id is required. Use list_servers to find server IDs."), nil
		}

		serverID, err := uuid.Parse(serverIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid server_id format — expected a UUID."), nil
		}

		// Verify it exists first for a better error message.
		srv, err := ss.GetByID(ctx, serverID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("server not found: %v. Use list_servers to see available servers.", err)), nil
		}

		if err := ss.Delete(ctx, serverID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete server: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Server '%s' (%s) deleted successfully.", srv.Hostname, serverIDStr)), nil
	}
}
