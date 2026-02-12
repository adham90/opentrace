package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// runDetailsTool returns the tool definition for get_run_details.
func runDetailsTool() mcp.Tool {
	return mcp.NewTool("get_run_details",
		mcp.WithDescription("Get the full execution trace for a watcher run: tool calls, AI reasoning, observations, timing, and alert outcome. Use when debugging why a watcher fired or missed something."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run UUID (from watcher_run_history)")),
	)
}

// runDetailsHandler returns a handler that fetches full run details.
func runDetailsHandler(ws store.WatcherStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		runIDStr, _ := args["run_id"].(string)
		if runIDStr == "" {
			return mcp.NewToolResultError("run_id is required"), nil
		}

		runID, err := uuid.Parse(runIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid run_id format"), nil
		}

		run, err := rs.GetByID(ctx, runID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("run not found: %v", err)), nil
		}

		// Fetch watcher info for context.
		var watcherInfo map[string]any
		if ws != nil {
			w, err := ws.GetByID(ctx, run.WatcherID)
			if err == nil {
				watcherInfo = map[string]any{
					"id":    w.ID.String(),
					"title": w.Title,
					"type":  string(w.WatcherType),
				}
			}
		}

		// Build run metadata.
		runMeta := map[string]any{
			"id":         run.ID.String(),
			"watcher_id": run.WatcherID.String(),
			"status":     run.Status,
			"has_alert":  run.HasAlert,
			"run_type":   run.RunType,
			"started_at": run.StartedAt.Format(time.RFC3339),
		}
		if run.FinishedAt != nil {
			runMeta["finished_at"] = run.FinishedAt.Format(time.RFC3339)
			runMeta["duration_ms"] = run.FinishedAt.Sub(run.StartedAt).Milliseconds()
		}
		if run.Summary != nil {
			runMeta["summary"] = *run.Summary
		}
		if run.Error != nil {
			runMeta["error"] = *run.Error
		}
		if run.ParentAlertID != nil {
			runMeta["parent_alert_id"] = *run.ParentAlertID
		}

		// Parse details JSON into structured events if possible.
		var parsedDetails any
		if run.Details != nil {
			var detailsMap any
			if err := json.Unmarshal(run.Details, &detailsMap); err == nil {
				parsedDetails = detailsMap
			} else {
				parsedDetails = string(run.Details)
			}
		}

		resp := map[string]any{
			"run": runMeta,
		}
		if watcherInfo != nil {
			resp["watcher"] = watcherInfo
		}
		if parsedDetails != nil {
			resp["details"] = parsedDetails
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
