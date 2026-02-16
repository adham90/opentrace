package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/store"
)

// getSettingsHandler returns a handler that retrieves current settings.
func getSettingsHandler(ss store.SettingsStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		retention, err := ss.GetRetention(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read retention settings: %v", err)), nil
		}

		resp := map[string]any{
			"retention_days": retention.RetentionDays,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal settings: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// updateRetentionHandler returns a handler that updates the data retention period.
func updateRetentionHandler(ss store.SettingsStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		daysF, ok := args["retention_days"].(float64)
		if !ok || daysF < 1 || daysF > 365 {
			return mcp.NewToolResultError("retention_days is required and must be between 1 and 365"), nil
		}
		days := int(daysF)

		if err := ss.SetRetention(ctx, store.RetentionSettings{RetentionDays: days}); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to update retention: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Data retention updated to %d days. Logs, alerts, and watcher runs older than %d days will be pruned on the next cleanup cycle.", days, days)), nil
	}
}
