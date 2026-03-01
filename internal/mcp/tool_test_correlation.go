package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// testGapsHandler returns a handler for the test_gaps tool.
// Lists uncovered production error paths ranked by impact/priority.
func testGapsHandler(tcs store.TestCorrelationStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		service, _ := args["service"].(string)

		limit := 10
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		paths, err := tcs.TopByPriority(ctx, service, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query test gaps: %v", err)), nil
		}

		if len(paths) == 0 {
			return mcp.NewToolResultText("No uncovered error paths found."), nil
		}

		result := map[string]any{
			"count": len(paths),
			"paths": paths,
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// testPriorityHandler returns a handler for the test_priority tool.
// Provides detailed context for a specific error fingerprint to help write tests.
func testPriorityHandler(tcs store.TestCorrelationStore, egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		fingerprint, _ := args["fingerprint"].(string)
		if fingerprint == "" {
			return mcp.NewToolResultError("fingerprint parameter is required"), nil
		}

		result := map[string]any{
			"fingerprint": fingerprint,
		}

		// Get uncovered path info
		path, err := tcs.GetByFingerprint(ctx, fingerprint)
		if err == nil && path != nil {
			result["uncovered_path"] = path
		}

		// Get error group detail
		if egs != nil {
			group, err := egs.Get(ctx, fingerprint)
			if err == nil && group != nil {
				result["error_group"] = group
			}

			// Get recent events
			events, err := egs.ListEvents(ctx, fingerprint, 5)
			if err == nil && len(events) > 0 {
				result["recent_events"] = events
			}
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// testGapsToolDef returns the mcp.Tool definition for test_gaps.
func testGapsToolDef() mcp.Tool {
	return mcp.NewTool("test_gaps",
		mcp.WithDescription(
			"List production error paths that lack test coverage, ranked by impact. "+
				"Shows which errors happen most in production and aren't covered by tests. "+
				"Use to prioritize what tests to write next.",
		),
		mcp.WithString("service",
			mcp.Description("Filter by service (optional)")),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 10)")),
	)
}

// testPriorityToolDef returns the mcp.Tool definition for test_priority.
func testPriorityToolDef() mcp.Tool {
	return mcp.NewTool("test_priority",
		mcp.WithDescription(
			"Get detailed context for a specific error to help write a test for it. "+
				"Returns the error group, recent occurrences, and source file info.",
		),
		mcp.WithString("fingerprint", mcp.Required(),
			mcp.Description("Error fingerprint (from test_gaps or error_groups)")),
	)
}
