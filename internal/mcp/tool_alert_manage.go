package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// acknowledgeAlertHandler returns a handler that marks an alert as read.
func acknowledgeAlertHandler(as store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		alertIDStr, _ := args["alert_id"].(string)
		if alertIDStr == "" {
			return mcp.NewToolResultError("alert_id is required. Use list_alerts to find alert IDs."), nil
		}

		alertID, err := uuid.Parse(alertIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid alert_id format — expected a UUID (e.g. from list_alerts)."), nil
		}

		// Verify the alert exists first.
		alert, err := as.GetByID(ctx, alertID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("alert not found: %v. Use list_alerts to see available alerts.", err)), nil
		}

		if alert.Read {
			return mcp.NewToolResultText(fmt.Sprintf("Alert '%s' is already acknowledged.", alert.Title)), nil
		}

		if err := as.MarkRead(ctx, alertID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to acknowledge alert: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Alert '%s' (severity: %s) acknowledged successfully.", alert.Title, alert.Severity)), nil
	}
}

// acknowledgeAllAlertsHandler returns a handler that marks all alerts as read.
func acknowledgeAllAlertsHandler(as store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := as.MarkAllRead(ctx); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to acknowledge alerts: %v", err)), nil
		}

		return mcp.NewToolResultText("All alerts acknowledged."), nil
	}
}

// dismissAlertHandler returns a handler that dismisses an alert.
func dismissAlertHandler(as store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		alertIDStr, _ := args["alert_id"].(string)
		if alertIDStr == "" {
			return mcp.NewToolResultError("alert_id is required. Use list_alerts to find alert IDs."), nil
		}

		alertID, err := uuid.Parse(alertIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid alert_id format — expected a UUID (e.g. from list_alerts)."), nil
		}

		alert, err := as.GetByID(ctx, alertID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("alert not found: %v. Use list_alerts to see available alerts.", err)), nil
		}

		if alert.Dismissed {
			return mcp.NewToolResultText(fmt.Sprintf("Alert '%s' is already dismissed.", alert.Title)), nil
		}

		reason, _ := args["reason"].(string)

		if err := as.Dismiss(ctx, alertID, reason); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to dismiss alert: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Alert '%s' (severity: %s) dismissed successfully.", alert.Title, alert.Severity)), nil
	}
}

// dismissAllAlertsHandler returns a handler that dismisses all alerts.
func dismissAllAlertsHandler(as store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := as.DismissAll(ctx); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to dismiss alerts: %v", err)), nil
		}

		return mcp.NewToolResultText("All alerts dismissed."), nil
	}
}
