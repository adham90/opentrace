package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func HandleTraffic(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	sinceStr := ArgStringDefault(args, "since", "24h")

	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	summary, err := d.AnalyticsStore.TrafficSummary(ctx, store.AnalyticsParams{
		Service: service,
		Since:   since,
		Until:   now,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get traffic summary: %v", err)), nil
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

	return JSONResult(resp,
		Suggest("analytics", "See which endpoints get the most traffic", map[string]any{
			"action": "endpoints",
			"since":  sinceStr,
		}),
		Suggest("analytics", "View metric trends over time", map[string]any{
			"action": "trends",
			"metric": "error_rate", "since": sinceStr,
		}),
		Suggest("errors", "See current application errors", map[string]any{"action": "list"}),
	)
}

func HandleEndpoints(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	sortBy := ArgStringDefault(args, "sort_by", "request_count")
	sinceStr := ArgStringDefault(args, "since", "24h")
	limit := ArgInt(args, "limit", 20, 100)
	minRequests := ArgInt(args, "min_requests", 5, 1000)

	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	endpoints, err := d.AnalyticsStore.TopEndpoints(ctx, store.TopEndpointParams{
		Service:     service,
		Since:       since,
		Until:       now,
		SortBy:      sortBy,
		Limit:       limit,
		MinRequests: minRequests,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get endpoints: %v", err)), nil
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

	return JSONResult(resp,
		Suggest("logs", "Drill into a specific endpoint's performance", map[string]any{
			"action":     "performance",
			"time_range": sinceStr,
		}),
		Suggest("analytics", "View trend over time for a metric", map[string]any{
			"action": "trends",
			"metric": "p95_response", "since": sinceStr,
		}),
	)
}
