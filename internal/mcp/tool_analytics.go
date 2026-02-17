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

// webAnalyticsHandler returns a handler for the web_analytics MCP tool.
func webAnalyticsHandler(as store.AnalyticsStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		service, _ := args["service"].(string)

		sinceStr := "24h"
		if v, ok := args["since"].(string); ok && v != "" {
			sinceStr = v
		}

		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}

		now := time.Now().UTC()
		since := now.Add(-duration)

		summary, err := as.TrafficSummary(ctx, store.AnalyticsParams{
			Service: service,
			Since:   since,
			Until:   now,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get traffic summary: %v", err)), nil
		}

		resp := map[string]any{
			"time_range": map[string]any{
				"start": since.Format(time.RFC3339),
				"end":   now.Format(time.RFC3339),
			},
			"summary": map[string]any{
				"total_requests":   summary.TotalRequests,
				"unique_endpoints": summary.UniqueEndpoints,
				"error_rate":       round2(summary.ErrorRate * 100),
				"avg_duration_ms":  round2(summary.AvgDurationMs),
				"p95_duration_ms":  round2(summary.P95DurationMs),
			},
			"status_breakdown": summary.StatusBreakdown,
			"method_breakdown": summary.MethodBreakdown,
		}

		resp = withSuggestions(resp,
			suggest("top_endpoints", "See which endpoints get the most traffic", map[string]any{
				"since": sinceStr,
			}),
			suggest("trends", "View metric trends over time", map[string]any{
				"metric": "error_rate", "since": sinceStr,
			}),
			suggest("error_groups", "See current application errors", nil),
		)

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// topEndpointsHandler returns a handler for the top_endpoints MCP tool.
func topEndpointsHandler(as store.AnalyticsStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		service, _ := args["service"].(string)
		sortBy, _ := args["sort_by"].(string)
		if sortBy == "" {
			sortBy = "request_count"
		}

		sinceStr := "24h"
		if v, ok := args["since"].(string); ok && v != "" {
			sinceStr = v
		}

		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		minRequests := 5
		if v, ok := args["min_requests"].(float64); ok && v > 0 {
			minRequests = int(v)
		}

		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}

		now := time.Now().UTC()
		since := now.Add(-duration)

		endpoints, err := as.TopEndpoints(ctx, store.TopEndpointParams{
			Service:     service,
			Since:       since,
			Until:       now,
			SortBy:      sortBy,
			Limit:       limit,
			MinRequests: minRequests,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get endpoints: %v", err)), nil
		}

		type endpointResult struct {
			Method          string         `json:"method"`
			Endpoint        string         `json:"endpoint"`
			PathPattern     string         `json:"path_pattern,omitempty"`
			RequestCount    int            `json:"request_count"`
			ErrorRate       float64        `json:"error_rate"`
			AvgDurationMs   float64        `json:"avg_duration_ms"`
			P95DurationMs   float64        `json:"p95_duration_ms"`
			AvgSQLCount     float64        `json:"avg_sql_count"`
			StatusBreakdown map[string]int `json:"status_breakdown"`
		}

		var results []endpointResult
		for _, e := range endpoints {
			errRate := float64(0)
			if e.RequestCount > 0 {
				errRate = float64(e.ErrorCount) / float64(e.RequestCount) * 100
			}
			results = append(results, endpointResult{
				Method:        e.Method,
				Endpoint:      e.Controller + "#" + e.Action,
				PathPattern:   e.PathPattern,
				RequestCount:  e.RequestCount,
				ErrorRate:     round2(errRate),
				AvgDurationMs: round2(e.AvgDurationMs),
				P95DurationMs: round2(e.P95DurationMs),
				AvgSQLCount:   round2(e.AvgSQLCount),
				StatusBreakdown: map[string]int{
					"2xx": e.Status2xx,
					"3xx": e.Status3xx,
					"4xx": e.Status4xx,
					"5xx": e.Status5xx,
				},
			})
		}

		resp := map[string]any{
			"sort_by":   sortBy,
			"endpoints": results,
		}

		resp = withSuggestions(resp,
			suggest("request_performance", "Drill into a specific endpoint's performance", map[string]any{
				"time_range": sinceStr,
			}),
			suggest("trends", "View trend over time for a metric", map[string]any{
				"metric": "p95_response", "since": sinceStr,
			}),
		)

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// trafficHeatmapHandler returns a handler for the traffic_heatmap MCP tool.
func trafficHeatmapHandler(as store.AnalyticsStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		service, _ := args["service"].(string)
		metricField := "request_count"
		if v, ok := args["metric"].(string); ok && v != "" {
			metricField = v
		}

		cells, err := as.TrafficHeatmap(ctx, service)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get heatmap: %v", err)), nil
		}

		dayNames := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

		type cellResult struct {
			Day   string  `json:"day"`
			Hour  int     `json:"hour"`
			Value float64 `json:"value"`
		}

		var results []cellResult
		var peak, quietest *cellResult
		for _, c := range cells {
			val := float64(c.RequestCount)
			switch metricField {
			case "error_count":
				val = float64(c.ErrorCount)
			case "avg_duration":
				val = c.AvgDurationMs
			}

			r := cellResult{
				Day:   dayNames[c.DayOfWeek],
				Hour:  c.HourOfDay,
				Value: round2(val),
			}
			results = append(results, r)

			if peak == nil || val > peak.Value {
				peak = &cellResult{r.Day, r.Hour, r.Value}
			}
			if quietest == nil || val < quietest.Value {
				quietest = &cellResult{r.Day, r.Hour, r.Value}
			}
		}

		resp := map[string]any{
			"metric": metricField,
			"cells":  results,
		}
		if peak != nil {
			resp["peak"] = peak
		}
		if quietest != nil {
			resp["quietest"] = quietest
		}

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}
