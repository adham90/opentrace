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

// logSearchHandler returns a handler that searches log entries with full-text
// search and filters. Returns individual log entries (unlike log_stats which
// returns aggregated counts).
func logSearchHandler(ls store.LogStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		query, _ := args["query"].(string)
		service, _ := args["service"].(string)
		level, _ := args["level"].(string)
		traceID, _ := args["trace_id"].(string)
		environment, _ := args["environment"].(string)

		limit := 50
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
			if limit > 200 {
				limit = 200
			}
		}

		offset := 0
		if v, ok := args["offset"].(float64); ok && v > 0 {
			offset = int(v)
		}

		params := store.LogSearchParams{
			Query:       query,
			Service:     service,
			Level:       level,
			TraceID:     traceID,
			Environment: environment,
			Limit:       limit,
			Offset:      offset,
		}

		// Parse time range.
		if v, ok := args["time_range"].(string); ok && v != "" {
			duration, err := parseTimeRange(v)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v. Use formats like '15m', '1h', '6h', '24h', '7d'.", err)), nil
			}
			now := time.Now().UTC()
			start := now.Add(-duration)
			params.Start = &start
			params.End = &now
		}

		entries, err := ls.Search(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to search logs: %v. Verify your query syntax and filters.", err)), nil
		}

		if len(entries) == 0 {
			hint := "No log entries found matching your criteria."
			if query != "" {
				hint += " Try broadening your search query or extending the time_range."
			}
			if level != "" {
				hint += fmt.Sprintf(" Level filter '%s' is active — try removing it.", level)
			}
			return mcp.NewToolResultText(hint), nil
		}

		// Build compact response entries.
		type logResult struct {
			ID          int64          `json:"id"`
			Timestamp   string         `json:"timestamp"`
			Level       string         `json:"level"`
			Service     string         `json:"service,omitempty"`
			TraceID     string         `json:"trace_id,omitempty"`
			Message     string         `json:"message"`
			Environment string         `json:"environment,omitempty"`
			Metadata    map[string]any `json:"metadata,omitempty"`
		}

		results := make([]logResult, 0, len(entries))
		for _, e := range entries {
			msg := e.Message
			if len(msg) > 500 {
				msg = msg[:500] + "..."
			}
			results = append(results, logResult{
				ID:          e.ID,
				Timestamp:   e.Timestamp.Format(time.RFC3339Nano),
				Level:       e.Level,
				Service:     e.Service,
				TraceID:     e.TraceID,
				Message:     msg,
				Environment: e.Environment,
				Metadata:    e.Metadata,
			})
		}

		resp := map[string]any{
			"total_returned": len(results),
			"entries":        results,
		}

		if len(results) == limit {
			resp["has_more"] = true
			resp["next_offset"] = offset + limit
			resp["hint"] = "More results may be available. Use the 'offset' parameter to paginate."
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
