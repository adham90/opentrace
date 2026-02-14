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
	"github.com/adham90/opentrace/internal/watcher"
)

// updateWatcherHandler returns a handler that updates an existing watcher.
func updateWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		watcherIDStr, _ := args["watcher_id"].(string)
		if watcherIDStr == "" {
			return mcp.NewToolResultError("watcher_id is required. Use list_watchers to find watcher IDs."), nil
		}

		watcherID, err := uuid.Parse(watcherIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid watcher_id format — expected a UUID (e.g. from list_watchers)."), nil
		}

		// Verify watcher exists.
		existing, err := ws.GetByID(ctx, watcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("watcher not found: %v. Use list_watchers to see available watchers.", err)), nil
		}

		params := store.UpdateWatcherParams{}
		changed := false

		if v, ok := args["title"].(string); ok && v != "" {
			params.Title = &v
			changed = true
		}
		if v, ok := args["description"].(string); ok && v != "" {
			params.Description = &v
			changed = true
		}
		if v, ok := args["severity"].(string); ok && v != "" {
			sev := store.WatcherSeverity(v)
			params.Severity = &sev
			changed = true
		}
		if v, ok := args["time_range"].(string); ok && v != "" {
			params.TimeRange = &v
			changed = true
		}
		if v, ok := args["schedule"].(string); ok && v != "" {
			if _, err := watcher.ParseSchedule(v); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid schedule expression: %v", err)), nil
			}
			params.Schedule = &v
			changed = true
		}
		if v, ok := args["effort"].(string); ok && v != "" {
			eff := store.WatcherEffort(v)
			params.Effort = &eff
			changed = true
		}

		// Parse rule_config JSON string for rule watchers.
		if rcStr, ok := args["rule_config"].(string); ok && rcStr != "" {
			if existing.WatcherType != store.WatcherTypeRule {
				return mcp.NewToolResultError("rule_config can only be set on rule watchers, not AI watchers."), nil
			}
			var rc store.RuleConfig
			if err := json.Unmarshal([]byte(rcStr), &rc); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid rule_config JSON: %v", err)), nil
			}
			params.RuleConfig = &rc
			changed = true
		}

		// Parse human_summary JSON string.
		if hsStr, ok := args["human_summary"].(string); ok && hsStr != "" {
			var hs store.WatcherHumanSummary
			if err := json.Unmarshal([]byte(hsStr), &hs); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid human_summary JSON: %v", err)), nil
			}
			params.HumanSummary = &hs
			changed = true
		}

		// Parse expires_at: RFC3339 timestamp or "never" to clear.
		if eaStr, ok := args["expires_at"].(string); ok && eaStr != "" {
			if eaStr == "never" {
				params.ClearExpiresAt = true
			} else {
				t, err := time.Parse(time.RFC3339, eaStr)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("invalid expires_at — expected RFC3339 format or 'never': %v", err)), nil
				}
				params.ExpiresAt = &t
			}
			changed = true
		}

		if !changed {
			return mcp.NewToolResultError("no fields to update. Provide at least one of: title, description, severity, time_range, schedule, effort, rule_config, human_summary."), nil
		}

		updated, err := ws.Update(ctx, watcherID, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to update watcher: %v", err)), nil
		}

		data, err := json.MarshalIndent(updated, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' updated successfully:\n%s", updated.Title, string(data))), nil
	}
}

// deleteWatcherHandler returns a handler that deletes a watcher.
func deleteWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		watcherIDStr, _ := args["watcher_id"].(string)
		if watcherIDStr == "" {
			return mcp.NewToolResultError("watcher_id is required. Use list_watchers to find watcher IDs."), nil
		}

		watcherID, err := uuid.Parse(watcherIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid watcher_id format — expected a UUID (e.g. from list_watchers)."), nil
		}

		// Verify watcher exists and capture title for confirmation message.
		existing, err := ws.GetByID(ctx, watcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("watcher not found: %v. Use list_watchers to see available watchers.", err)), nil
		}

		if err := ws.Delete(ctx, watcherID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' (ID: %s) deleted successfully. Its historical alerts and run data remain intact.", existing.Title, watcherID)), nil
	}
}

// pauseWatcherHandler returns a handler that pauses a watcher.
func pauseWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		watcherIDStr, _ := args["watcher_id"].(string)
		if watcherIDStr == "" {
			return mcp.NewToolResultError("watcher_id is required. Use list_watchers to find watcher IDs."), nil
		}

		watcherID, err := uuid.Parse(watcherIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid watcher_id format — expected a UUID (e.g. from list_watchers)."), nil
		}

		existing, err := ws.GetByID(ctx, watcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("watcher not found: %v. Use list_watchers to see available watchers.", err)), nil
		}

		if existing.Status == store.WatcherPaused {
			return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' is already paused.", existing.Title)), nil
		}

		if _, err := ws.UpdateStatus(ctx, watcherID, store.WatcherPaused); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to pause watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' paused. Use resume_watcher to re-enable it.", existing.Title)), nil
	}
}

// resumeWatcherHandler returns a handler that resumes a paused watcher.
func resumeWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		watcherIDStr, _ := args["watcher_id"].(string)
		if watcherIDStr == "" {
			return mcp.NewToolResultError("watcher_id is required. Use list_watchers to find watcher IDs."), nil
		}

		watcherID, err := uuid.Parse(watcherIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid watcher_id format — expected a UUID (e.g. from list_watchers)."), nil
		}

		existing, err := ws.GetByID(ctx, watcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("watcher not found: %v. Use list_watchers to see available watchers.", err)), nil
		}

		if existing.Status == store.WatcherActive {
			return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' is already active.", existing.Title)), nil
		}

		if existing.Status == store.WatcherExpired {
			if err := ws.ResumeWatcher(ctx, watcherID); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to reactivate expired watcher: %v", err)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' reactivated (expiration cleared). It will run on its next scheduled time.", existing.Title)), nil
		}

		if err := ws.ResumeWatcher(ctx, watcherID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to resume watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' resumed and will run on its next scheduled time.", existing.Title)), nil
	}
}
