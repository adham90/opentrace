package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// snoozeAlertTool returns the tool definition for snooze_alert.
func snoozeAlertTool() mcp.Tool {
	return mcp.NewTool("snooze_alert",
		mcp.WithDescription("Temporarily suppress an alert for a specified duration. The alert will reappear after the snooze period expires. Useful for known issues that are being worked on."),
		mcp.WithString("alert_id", mcp.Required(), mcp.Description("Alert UUID (from list_alerts)")),
		mcp.WithString("duration", mcp.Required(), mcp.Description("How long to snooze: 1h, 4h, 8h, 1d, or 3d")),
	)
}

// snoozeAlertHandler returns a handler that snoozes an alert for a given duration.
func snoozeAlertHandler(as store.AlertStore) server.ToolHandlerFunc {
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

		durationStr, _ := args["duration"].(string)
		if durationStr == "" {
			return mcp.NewToolResultError("duration is required. Valid values: 1h, 4h, 8h, 1d, 3d."), nil
		}

		dur, err := parseSnoozeDuration(durationStr)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Verify the alert exists.
		alert, err := as.GetByID(ctx, alertID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("alert not found: %v. Use list_alerts to see available alerts.", err)), nil
		}

		snoozedUntil := time.Now().UTC().Add(dur)

		if err := as.Snooze(ctx, alertID, snoozedUntil); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to snooze alert: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Alert '%s' (severity: %s) snoozed until %s (%s from now).",
			alert.Title,
			alert.Severity,
			snoozedUntil.Format(time.RFC3339),
			durationStr,
		)), nil
	}
}

// parseSnoozeDuration converts a snooze duration string to a time.Duration.
// Accepted values: 1h, 4h, 8h, 1d, 3d.
func parseSnoozeDuration(s string) (time.Duration, error) {
	switch s {
	case "1h":
		return 1 * time.Hour, nil
	case "4h":
		return 4 * time.Hour, nil
	case "8h":
		return 8 * time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	case "3d":
		return 72 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration '%s'. Valid values: 1h, 4h, 8h, 1d, 3d", s)
	}
}
