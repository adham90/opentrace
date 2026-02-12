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

// compareRunsTool returns the tool definition for compare_watcher_runs.
func compareRunsTool() mcp.Tool {
	return mcp.NewTool("compare_watcher_runs",
		mcp.WithDescription("Compare two watcher runs side-by-side: status, alerts, duration, summaries, and tool usage differences. Useful for understanding what changed between runs or why one triggered an alert and another didn't."),
		mcp.WithString("run_id_a", mcp.Required(), mcp.Description("First run UUID (from watcher_run_history)")),
		mcp.WithString("run_id_b", mcp.Required(), mcp.Description("Second run UUID (from watcher_run_history)")),
	)
}

// compareRunsHandler returns a handler that compares two watcher runs.
func compareRunsHandler(ws store.WatcherStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		runAStr, _ := args["run_id_a"].(string)
		if runAStr == "" {
			return mcp.NewToolResultError("run_id_a is required"), nil
		}
		runBStr, _ := args["run_id_b"].(string)
		if runBStr == "" {
			return mcp.NewToolResultError("run_id_b is required"), nil
		}

		runAID, err := uuid.Parse(runAStr)
		if err != nil {
			return mcp.NewToolResultError("invalid run_id_a format — expected a UUID"), nil
		}
		runBID, err := uuid.Parse(runBStr)
		if err != nil {
			return mcp.NewToolResultError("invalid run_id_b format — expected a UUID"), nil
		}

		runA, err := rs.GetByID(ctx, runAID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("run A not found: %v", err)), nil
		}
		runB, err := rs.GetByID(ctx, runBID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("run B not found: %v", err)), nil
		}

		// Status comparison.
		statusComp := map[string]any{
			"run_a": runA.Status,
			"run_b": runB.Status,
			"match": runA.Status == runB.Status,
		}

		// Alert comparison.
		alertComp := map[string]any{
			"run_a": runA.HasAlert,
			"run_b": runB.HasAlert,
			"match": runA.HasAlert == runB.HasAlert,
		}

		// Duration comparison.
		durationComp := map[string]any{}
		if runA.FinishedAt != nil {
			durationComp["run_a_ms"] = runA.FinishedAt.Sub(runA.StartedAt).Milliseconds()
		}
		if runB.FinishedAt != nil {
			durationComp["run_b_ms"] = runB.FinishedAt.Sub(runB.StartedAt).Milliseconds()
		}
		if runA.FinishedAt != nil && runB.FinishedAt != nil {
			durA := runA.FinishedAt.Sub(runA.StartedAt)
			durB := runB.FinishedAt.Sub(runB.StartedAt)
			durationComp["diff_ms"] = (durA - durB).Milliseconds()
		}

		// Summary comparison (first 500 chars of each).
		summaryComp := map[string]any{}
		if runA.Summary != nil {
			s := *runA.Summary
			if len(s) > 500 {
				s = s[:500] + "..."
			}
			summaryComp["run_a"] = s
		}
		if runB.Summary != nil {
			s := *runB.Summary
			if len(s) > 500 {
				s = s[:500] + "..."
			}
			summaryComp["run_b"] = s
		}

		// Tool calls diff: parse Details JSON from both and compare tool_name fields.
		toolsComp := compareToolCalls(runA.Details, runB.Details)

		// Timing info.
		timingComp := map[string]any{
			"run_a_started": runA.StartedAt.Format(time.RFC3339),
			"run_b_started": runB.StartedAt.Format(time.RFC3339),
		}

		resp := map[string]any{
			"run_a_id": runA.ID.String(),
			"run_b_id": runB.ID.String(),
			"status":   statusComp,
			"alerts":   alertComp,
			"duration": durationComp,
			"summary":  summaryComp,
			"tools":    toolsComp,
			"timing":   timingComp,
		}

		// If both runs belong to the same watcher, add watcher context.
		if runA.WatcherID == runB.WatcherID && ws != nil {
			w, err := ws.GetByID(ctx, runA.WatcherID)
			if err == nil {
				resp["watcher"] = map[string]any{
					"id":    w.ID.String(),
					"title": w.Title,
				}
			}
		} else {
			resp["note"] = "Runs belong to different watchers"
			resp["watcher_a_id"] = runA.WatcherID.String()
			resp["watcher_b_id"] = runB.WatcherID.String()
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal comparison: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// compareToolCalls extracts tool_name fields from run details and computes added/removed/shared sets.
func compareToolCalls(detailsA, detailsB json.RawMessage) map[string]any {
	toolsA := extractToolNames(detailsA)
	toolsB := extractToolNames(detailsB)

	setA := make(map[string]bool, len(toolsA))
	for _, t := range toolsA {
		setA[t] = true
	}
	setB := make(map[string]bool, len(toolsB))
	for _, t := range toolsB {
		setB[t] = true
	}

	var shared, addedInB, removedInB []string
	for t := range setA {
		if setB[t] {
			shared = append(shared, t)
		} else {
			removedInB = append(removedInB, t)
		}
	}
	for t := range setB {
		if !setA[t] {
			addedInB = append(addedInB, t)
		}
	}

	return map[string]any{
		"run_a_tools": toolsA,
		"run_b_tools": toolsB,
		"shared":      shared,
		"added_in_b":  addedInB,
		"removed_in_b": removedInB,
	}
}

// extractToolNames parses a run's Details JSON and extracts tool_name fields from arrays.
func extractToolNames(details json.RawMessage) []string {
	if details == nil {
		return nil
	}

	// Try parsing as an array of objects with tool_name fields.
	var arr []map[string]any
	if err := json.Unmarshal(details, &arr); err == nil {
		var names []string
		for _, item := range arr {
			if name, ok := item["tool_name"].(string); ok && name != "" {
				names = append(names, name)
			}
		}
		return names
	}

	// Try parsing as a map that may contain a nested array.
	var obj map[string]any
	if err := json.Unmarshal(details, &obj); err == nil {
		// Look for common keys that hold tool call arrays.
		for _, key := range []string{"tool_calls", "tools", "steps", "events"} {
			if items, ok := obj[key]; ok {
				if itemsJSON, err := json.Marshal(items); err == nil {
					var innerArr []map[string]any
					if err := json.Unmarshal(itemsJSON, &innerArr); err == nil {
						var names []string
						for _, item := range innerArr {
							if name, ok := item["tool_name"].(string); ok && name != "" {
								names = append(names, name)
							}
						}
						if len(names) > 0 {
							return names
						}
					}
				}
			}
		}
	}

	return nil
}
