package tools

import (
	"context"
	"encoding/json"
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
		action, _ := args["action"].(string)

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

func HandleTraffic(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	service, _ := args["service"].(string)

	sinceStr := "24h"
	if v, ok := args["since"].(string); ok && v != "" {
		sinceStr = v
	}

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

	resp = WithSuggestions(resp,
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

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func HandleEndpoints(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
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

	resp = WithSuggestions(resp,
		Suggest("logs", "Drill into a specific endpoint's performance", map[string]any{
			"action":     "performance",
			"time_range": sinceStr,
		}),
		Suggest("analytics", "View trend over time for a metric", map[string]any{
			"action": "trends",
			"metric": "p95_response", "since": sinceStr,
		}),
	)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func HandleHeatmap(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	service, _ := args["service"].(string)
	metricField := "request_count"
	if v, ok := args["metric"].(string); ok && v != "" {
		metricField = v
	}

	cells, err := d.AnalyticsStore.TrafficHeatmap(ctx, service)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get heatmap: %v", err)), nil
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

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func HandleTrends(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	metric, _ := args["metric"].(string)
	if metric == "" {
		metric = "request_volume"
	}

	interval := "1h"
	if v, ok := args["interval"].(string); ok && v != "" {
		interval = v
	}

	sinceStr := "24h"
	if v, ok := args["since"].(string); ok && v != "" {
		sinceStr = v
	}

	service, _ := args["service"].(string)
	endpoint, _ := args["endpoint"].(string)
	environment, _ := args["environment"].(string)
	compareTo, _ := args["compare_to"].(string)

	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	params := store.TrendQueryParams{
		Service:     service,
		Endpoint:    endpoint,
		Environment: environment,
		Interval:    interval,
		Metric:      metric,
		Since:       since,
		Until:       now,
	}

	buckets, err := d.TrendStore.QueryTrends(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query trends: %v", err)), nil
	}

	type dataPoint struct {
		Timestamp string  `json:"timestamp"`
		Value     float64 `json:"value"`
	}

	var points []dataPoint
	var sum float64
	for _, b := range buckets {
		val := extractMetric(b, metric)
		points = append(points, dataPoint{
			Timestamp: b.BucketStart.Format(time.RFC3339),
			Value:     round2(val),
		})
		sum += val
	}

	currentAvg := float64(0)
	if len(points) > 0 {
		currentAvg = sum / float64(len(points))
	}

	resp := map[string]any{
		"metric":     metric,
		"interval":   interval,
		"time_range": map[string]any{"start": since.Format(time.RFC3339), "end": now.Format(time.RFC3339)},
		"data_points": points,
		"summary": map[string]any{
			"current_avg": round2(currentAvg),
			"data_points": len(points),
		},
	}

	// Compare to baseline if requested
	if compareTo != "" {
		baselineSince, baselineUntil := computeBaseline(since, now, compareTo)
		baselineParams := store.TrendQueryParams{
			Service:     service,
			Endpoint:    endpoint,
			Environment: environment,
			Interval:    interval,
			Metric:      metric,
			Since:       baselineSince,
			Until:       baselineUntil,
		}

		baselineBuckets, err := d.TrendStore.QueryTrends(ctx, baselineParams)
		if err == nil && len(baselineBuckets) > 0 {
			var baselinePoints []dataPoint
			var baselineSum float64
			for _, b := range baselineBuckets {
				val := extractMetric(b, metric)
				baselinePoints = append(baselinePoints, dataPoint{
					Timestamp: b.BucketStart.Format(time.RFC3339),
					Value:     round2(val),
				})
				baselineSum += val
			}
			baselineAvg := baselineSum / float64(len(baselinePoints))
			resp["baseline_points"] = baselinePoints

			changePct := float64(0)
			if baselineAvg > 0 {
				changePct = (currentAvg - baselineAvg) / baselineAvg * 100
			}

			summary := resp["summary"].(map[string]any)
			summary["baseline_avg"] = round2(baselineAvg)
			summary["change_pct"] = round2(changePct)
			if changePct > 10 {
				summary["trend"] = "increasing"
			} else if changePct < -10 {
				summary["trend"] = "decreasing"
			} else {
				summary["trend"] = "stable"
			}
		}
	}

	// Add deploy markers
	markers, err := d.TrendStore.ListDeployMarkers(ctx, service, since)
	if err == nil && len(markers) > 0 {
		var deployList []map[string]any
		for _, m := range markers {
			deployList = append(deployList, map[string]any{
				"timestamp":   m.FirstSeenAt.Format(time.RFC3339),
				"commit_hash": m.CommitHash,
				"service":     m.Service,
			})
		}
		resp["deploy_markers"] = deployList
	}

	resp = WithSuggestions(resp,
		Suggest("analytics", "Find which endpoints changed the most", map[string]any{
			"action": "movers",
			"metric": metric, "since": sinceStr,
		}),
		Suggest("logs", "Drill into request-level performance", map[string]any{
			"action":     "performance",
			"time_range": sinceStr,
		}),
	)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func HandleMovers(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	metric, _ := args["metric"].(string)
	if metric == "" {
		metric = "p95_response"
	}

	sinceStr := "24h"
	if v, ok := args["since"].(string); ok && v != "" {
		sinceStr = v
	}

	baseline := "previous_period"
	if v, ok := args["baseline"].(string); ok && v != "" {
		baseline = v
	}

	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)
	baselineSince, baselineUntil := computeBaseline(since, now, baseline)

	currentBuckets, err := d.TrendStore.QueryTrends(ctx, store.TrendQueryParams{
		Interval: "1h",
		Since:    since,
		Until:    now,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query current: %v", err)), nil
	}

	baselineBuckets, err := d.TrendStore.QueryTrends(ctx, store.TrendQueryParams{
		Interval: "1h",
		Since:    baselineSince,
		Until:    baselineUntil,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query baseline: %v", err)), nil
	}

	type svcMetric struct {
		sum   float64
		count int
	}
	currentByService := make(map[string]*svcMetric)
	baselineByService := make(map[string]*svcMetric)

	for _, b := range currentBuckets {
		key := b.Service
		if key == "" {
			key = "(all)"
		}
		m, ok := currentByService[key]
		if !ok {
			m = &svcMetric{}
			currentByService[key] = m
		}
		m.sum += extractMetric(b, metric)
		m.count++
	}
	for _, b := range baselineBuckets {
		key := b.Service
		if key == "" {
			key = "(all)"
		}
		m, ok := baselineByService[key]
		if !ok {
			m = &svcMetric{}
			baselineByService[key] = m
		}
		m.sum += extractMetric(b, metric)
		m.count++
	}

	type mover struct {
		Name          string  `json:"name"`
		CurrentValue  float64 `json:"current_value"`
		BaselineValue float64 `json:"baseline_value"`
		ChangePct     float64 `json:"change_pct"`
	}

	movers := make([]mover, 0)
	for svc, cur := range currentByService {
		curAvg := cur.sum / float64(maxInt(cur.count, 1))
		baseAvg := float64(0)
		if base, ok := baselineByService[svc]; ok && base.count > 0 {
			baseAvg = base.sum / float64(base.count)
		}
		changePct := float64(0)
		if baseAvg > 0 {
			changePct = (curAvg - baseAvg) / baseAvg * 100
		}
		movers = append(movers, mover{
			Name:          svc,
			CurrentValue:  round2(curAvg),
			BaselineValue: round2(baseAvg),
			ChangePct:     round2(changePct),
		})
	}

	// Sort by absolute change descending
	for i := 0; i < len(movers); i++ {
		for j := i + 1; j < len(movers); j++ {
			if absFloat(movers[j].ChangePct) > absFloat(movers[i].ChangePct) {
				movers[i], movers[j] = movers[j], movers[i]
			}
		}
	}

	if len(movers) > limit {
		movers = movers[:limit]
	}

	resp := map[string]any{
		"metric":   metric,
		"baseline": baseline,
		"movers":   movers,
	}

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
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

func round2(f float64) float64 {
	return float64(int(f*100)) / 100
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
