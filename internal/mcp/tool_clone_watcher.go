package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// cloneWatcherTool returns the tool definition for clone_watcher.
func cloneWatcherTool() mcp.Tool {
	return mcp.NewTool("clone_watcher",
		mcp.WithDescription("Duplicate an existing watcher with optional overrides. Useful for creating similar watchers for different environments or with tweaked settings. The clone starts in active status."),
		mcp.WithString("watcher_id", mcp.Required(), mcp.Description("UUID of the watcher to clone (from list_watchers)")),
		mcp.WithString("title", mcp.Description("Title for the cloned watcher (default: 'Copy of {original title}')")),
		mcp.WithString("environment", mcp.Description("Override the environment (e.g. staging, production)")),
		mcp.WithString("schedule", mcp.Description("Override the schedule (cron expression or interval like '5m', '@hourly')")),
		mcp.WithString("severity", mcp.Description("Override the severity: info, warning, or critical")),
	)
}

// cloneWatcherHandler returns a handler that duplicates an existing watcher with optional overrides.
func cloneWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
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

		// Fetch the source watcher.
		source, err := ws.GetByID(ctx, watcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("watcher not found: %v. Use list_watchers to see available watchers.", err)), nil
		}

		// Build create params from the source watcher.
		params := store.CreateWatcherParams{
			Title:          fmt.Sprintf("Copy of %s", source.Title),
			Description:    source.Description,
			Environment:    source.Environment,
			Severity:       source.Severity,
			Filters:        source.Filters,
			TimeRange:      source.TimeRange,
			Schedule:       source.Schedule,
			Model:          source.Model,
			Effort:         source.Effort,
			Notify:         source.Notify,
			WatcherType:    source.WatcherType,
			RuleConfig:     source.RuleConfig,
			DataSourceID:   source.DataSourceID,
			AdaptiveConfig: source.AdaptiveConfig,
			HumanSummary:   source.HumanSummary,
		}

		// Apply overrides.
		if v, ok := args["title"].(string); ok && v != "" {
			params.Title = v
		}
		if v, ok := args["environment"].(string); ok && v != "" {
			params.Environment = v
		}
		if v, ok := args["schedule"].(string); ok && v != "" {
			params.Schedule = v
		}
		if v, ok := args["severity"].(string); ok && v != "" {
			params.Severity = store.WatcherSeverity(v)
		}

		cloned, err := ws.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to clone watcher: %v", err)), nil
		}

		data, err := json.MarshalIndent(cloned, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal cloned watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Watcher '%s' cloned from '%s' successfully:\n%s", cloned.Title, source.Title, string(data))), nil
	}
}
