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

// promoteInvestigationTool returns the tool definition for promote_investigation.
func promoteInvestigationTool() mcp.Tool {
	return mcp.NewTool("promote_investigation",
		mcp.WithDescription("Create a recurring watcher from a one-off investigation. Takes an alert or run ID, copies the parent watcher's configuration, and creates a new watcher to continuously monitor the same pattern. Useful when an ad-hoc investigation reveals something worth monitoring long-term."),
		mcp.WithString("alert_id", mcp.Description("Alert UUID to promote from (from list_alerts or alert_details). At least one of alert_id or run_id is required.")),
		mcp.WithString("run_id", mcp.Description("Run UUID to promote from (from run_history). At least one of alert_id or run_id is required.")),
		mcp.WithString("title", mcp.Description("Title for the new watcher (default: 'Monitor: {parent watcher title}')")),
		mcp.WithString("schedule", mcp.Description("Schedule for the new watcher (default: parent's schedule or '15m')")),
	)
}

// promoteInvestigationHandler returns a handler that creates a recurring watcher from an investigation.
func promoteInvestigationHandler(ws store.WatcherStore, as store.AlertStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		alertIDStr, _ := args["alert_id"].(string)
		runIDStr, _ := args["run_id"].(string)
		title, _ := args["title"].(string)
		schedule, _ := args["schedule"].(string)

		if alertIDStr == "" && runIDStr == "" {
			return mcp.NewToolResultError("at least one of alert_id or run_id is required"), nil
		}

		// Determine the parent watcher ID from the alert or run.
		var parentWatcherID uuid.UUID
		var investigationContext string

		if alertIDStr != "" {
			alertID, err := uuid.Parse(alertIDStr)
			if err != nil {
				return mcp.NewToolResultError("invalid alert_id format — expected a UUID"), nil
			}

			alert, err := as.GetByID(ctx, alertID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("alert not found: %v", err)), nil
			}

			if alert.WatcherID == nil {
				return mcp.NewToolResultError("alert has no associated watcher — cannot promote"), nil
			}
			parentWatcherID = *alert.WatcherID
			investigationContext = fmt.Sprintf("Promoted from alert '%s' (ID: %s)", alert.Title, alertID)
		} else {
			runID, err := uuid.Parse(runIDStr)
			if err != nil {
				return mcp.NewToolResultError("invalid run_id format — expected a UUID"), nil
			}

			run, err := rs.GetByID(ctx, runID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("run not found: %v", err)), nil
			}

			parentWatcherID = run.WatcherID
			investigationContext = fmt.Sprintf("Promoted from run ID: %s", runID)
		}

		// Fetch the parent watcher to copy its configuration.
		parent, err := ws.GetByID(ctx, parentWatcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("parent watcher not found: %v", err)), nil
		}

		// Build CreateWatcherParams from the parent watcher.
		params := store.CreateWatcherParams{
			Title:       fmt.Sprintf("Monitor: %s", parent.Title),
			Description: parent.Description,
			Severity:    parent.Severity,
			Filters:        parent.Filters,
			TimeRange:      parent.TimeRange,
			Schedule:       parent.Schedule,
			Model:          parent.Model,
			Effort:         parent.Effort,
			Notify:         parent.Notify,
			WatcherType:    parent.WatcherType,
			RuleConfig:     parent.RuleConfig,
			DataSourceID:   parent.DataSourceID,
			AdaptiveConfig: parent.AdaptiveConfig,
			HumanSummary:   parent.HumanSummary,
		}

		// Apply overrides.
		if title != "" {
			params.Title = title
		}

		// Set schedule: use provided value, fall back to parent's schedule, then default to "15m".
		if schedule != "" {
			params.Schedule = schedule
		} else if params.Schedule == "" {
			params.Schedule = "15m"
		}

		// Enrich description with investigation context.
		if params.Description != "" {
			params.Description = fmt.Sprintf("%s\n\n---\n%s", params.Description, investigationContext)
		} else {
			params.Description = investigationContext
		}

		// Create the new watcher.
		created, err := ws.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create watcher: %v", err)), nil
		}

		data, err := json.MarshalIndent(created, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Investigation promoted to recurring watcher '%s' (ID: %s).\n%s", created.Title, created.ID, string(data))), nil
	}
}
