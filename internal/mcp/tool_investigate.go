package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// investigateAlertTool returns the tool definition.
func investigateAlertTool() mcp.Tool {
	return mcp.NewTool("investigate_alert",
		mcp.WithDescription("Trigger a deep AI investigation of a specific alert. Creates a temporary AI watcher that investigates root cause, checks for cascading failures, and provides remediation steps. The investigation runs asynchronously — use get_run_details to check progress."),
		mcp.WithString("alert_id", mcp.Required(), mcp.Description("Alert UUID to investigate (from list_alerts)")),
	)
}

// investigateAlertHandler returns a handler that triggers alert investigation.
func investigateAlertHandler(
	alertStore store.AlertStore,
	watcherStore store.WatcherStore,
	executor *watcher.Executor,
) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		alertIDStr, _ := args["alert_id"].(string)
		if alertIDStr == "" {
			return mcp.NewToolResultError("alert_id is required. Use list_alerts to find alert IDs."), nil
		}

		alertID, err := uuid.Parse(alertIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid alert_id format — expected a UUID."), nil
		}

		alert, err := alertStore.GetByID(ctx, alertID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("alert not found: %v. Use list_alerts to see available alerts.", err)), nil
		}

		// Build investigation prompt
		prompt := fmt.Sprintf(`INVESTIGATION MODE: Deep follow-up investigation.

ORIGINAL ALERT:
Title: %s
Severity: %s
Summary: %s
Time: %s

YOUR TASK:
1. Verify if the issue is still present
2. Investigate root cause with deeper analysis
3. Check for related/cascading failures
4. Provide actionable remediation steps
5. Assess: getting worse, stable, or improving?

Use all available tools to gather evidence. Be thorough and systematic.`,
			alert.Title,
			alert.Severity,
			alert.Summary,
			alert.CreatedAt.Format("2006-01-02 15:04:05"),
		)

		// Get original watcher's data_source_id if available
		var dataSourceID *string
		if alert.WatcherID != nil {
			origWatcher, err := watcherStore.GetByID(ctx, *alert.WatcherID)
			if err == nil && origWatcher.DataSourceID != nil {
				dataSourceID = origWatcher.DataSourceID
			}
		}

		// Create a temporary investigation watcher
		params := store.CreateWatcherParams{
			Title:       fmt.Sprintf("Investigation: %s", alert.Title),
			Description: prompt,
			Severity:    alert.Severity,
			Filters:     json.RawMessage(`{}`),
			TimeRange:   "30m",
			Effort:      store.EffortHigh,
			WatcherType: store.WatcherTypeAI,
			Notify:      json.RawMessage(`["dashboard"]`),
			DataSourceID: func() *string {
				if dataSourceID != nil {
					return dataSourceID
				}
				return nil
			}(),
		}

		w, err := watcherStore.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create investigation watcher: %v", err)), nil
		}

		// Pause immediately so the scheduler doesn't pick it up
		watcherStore.UpdateStatus(ctx, w.ID, store.WatcherPaused)

		// Execute asynchronously
		go func() {
			executor.Execute(context.Background(), *w)
		}()

		result := map[string]any{
			"status":     "triggered",
			"watcher_id": w.ID.String(),
			"message":    fmt.Sprintf("Investigation started for alert '%s'. Use get_run_details or watcher_run_history with watcher_id=%s to check progress.", alert.Title, w.ID.String()),
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
