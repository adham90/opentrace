package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// getAuditLogTool returns the tool definition.
func getAuditLogTool() mcp.Tool {
	return mcp.NewTool("get_audit_log",
		mcp.WithDescription("View recent admin actions for security review and change tracking. Shows who did what and when — user creation, connector changes, setting updates, etc. Admin only."),
		mcp.WithNumber("limit", mcp.Description("Maximum number of entries to return (default: 50, max: 200)")),
	)
}

// getAuditLogHandler returns a handler that lists recent audit entries.
func getAuditLogHandler(as store.AuditStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		limit := 50
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		if limit > 200 {
			limit = 200
		}

		entries, err := as.Recent(ctx, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to fetch audit log: %v", err)), nil
		}

		if len(entries) == 0 {
			return mcp.NewToolResultText("No audit log entries found."), nil
		}

		data, err := json.Marshal(entries)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal audit log: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
