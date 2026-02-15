package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// listLogAttributesHandler returns a handler that discovers distinct values
// for log fields (service, level) or lists metadata keys.
// This is the bootstrapping tool — Claude calls it first to learn what's
// available before filtering.
func listLogAttributesHandler(ls store.LogStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		field, _ := args["field"].(string)
		if field == "" {
			return mcp.NewToolResultError("field is required (service, level, event_type, environment, commit_hash, request_id, exception_class, error_fingerprint, source_file, or metadata_key)"), nil
		}

		// Parse time range (default: 24h).
		timeRange := "24h"
		if v, ok := args["time_range"].(string); ok && v != "" {
			timeRange = v
		}
		duration, err := parseTimeRange(timeRange)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
		}
		now := time.Now().UTC()
		params := store.LogCountParams{
			Since: now.Add(-duration),
			Until: now,
		}
		if v, ok := args["service"].(string); ok && v != "" {
			params.Service = v
		}

		if field == "metadata_key" {
			keys, err := ls.MetadataKeys(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to list metadata keys: %v", err)), nil
			}
			if len(keys) == 0 {
				return mcp.NewToolResultText("No metadata keys found in the specified time range."), nil
			}
			resp := map[string]any{
				"field":  "metadata_key",
				"count":  len(keys),
				"values": keys,
				"hint":   "Use these keys with the metadata_filter parameter in log_search (e.g. metadata_filter: {\"host\": \"server-01\"}).",
			}
			data, _ := json.MarshalIndent(resp, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		values, err := ls.DistinctValues(ctx, field, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list values: %v", err)), nil
		}

		if len(values) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No %s values found in the specified time range.", field)), nil
		}

		resp := map[string]any{
			"field":  field,
			"count":  len(values),
			"values": values,
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}
