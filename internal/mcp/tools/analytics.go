package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// AnalyticsDeps holds the stores needed by the analytics tool.
type AnalyticsDeps struct {
	AnalyticsStore store.AnalyticsStore
	TrendStore     store.TrendStore
}

// AnalyticsHandler returns a handler for the consolidated analytics tool.
func AnalyticsHandler(d AnalyticsDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "traffic":
			return HandleTraffic(ctx, d, args)
		case "endpoints":
			return HandleEndpoints(ctx, d, args)
		case "heatmap":
			return HandleHeatmap(ctx, d, args)
		case "trends":
			return HandleTrends(ctx, d, args)
		case "movers":
			return HandleMovers(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use traffic, endpoints, heatmap, trends, movers)", action)), nil
		}
	}
}

// --- helpers ---

// ParseTimeRange converts strings like "1h", "30m", "24h", "7d" to a duration.
func ParseTimeRange(s string) (time.Duration, error) {
	if s == "" {
		return time.Hour, nil
	}

	// Try standard Go duration first.
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}

	// Handle "d" suffix.
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(numStr, "%d", &days); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}

	return 0, fmt.Errorf("invalid duration: %s", s)
}

func extractMetric(b store.MetricBucket, metric string) float64 {
	switch metric {
	case "error_rate":
		if b.RequestCount > 0 {
			return float64(b.ErrorCount) / float64(b.RequestCount) * 100
		}
		return 0
	case "p95_response":
		return b.P95DurationMs
	case "avg_response":
		return b.AvgDurationMs
	case "request_volume":
		return float64(b.RequestCount)
	case "avg_sql_count":
		return b.AvgSQLCount
	case "avg_db_time":
		return b.AvgDBTimeMs
	case "cache_hit_ratio":
		return b.AvgCacheHitRatio
	case "error_count":
		return float64(b.ErrorCount)
	default:
		return float64(b.RequestCount)
	}
}

func computeBaseline(since, until time.Time, compareTo string) (time.Time, time.Time) {
	duration := until.Sub(since)
	switch compareTo {
	case "previous_period":
		return since.Add(-duration), since
	case "previous_week":
		return since.Add(-7 * 24 * time.Hour), until.Add(-7 * 24 * time.Hour)
	default:
		return since.Add(-duration), since
	}
}
