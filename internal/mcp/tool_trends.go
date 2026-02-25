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

// trendsHandler returns a handler for the trends MCP tool.
func trendsHandler(ts store.TrendStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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

		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
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

		buckets, err := ts.QueryTrends(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query trends: %v", err)), nil
		}

		// Extract the requested metric from buckets
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

			baselineBuckets, err := ts.QueryTrends(ctx, baselineParams)
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
		markers, err := ts.ListDeployMarkers(ctx, service, since)
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

		resp = withSuggestions(resp,
			suggest("top_movers", "Find which endpoints changed the most", map[string]any{
				"metric": metric, "since": sinceStr,
			}),
			suggest("request_performance", "Drill into request-level performance", map[string]any{
				"time_range": sinceStr,
			}),
		)

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// topMoversHandler finds endpoints or services with the biggest metric changes.
func topMoversHandler(ts store.TrendStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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

		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}

		now := time.Now().UTC()
		since := now.Add(-duration)
		baselineSince, baselineUntil := computeBaseline(since, now, baseline)

		// Get current period buckets
		currentBuckets, err := ts.QueryTrends(ctx, store.TrendQueryParams{
			Interval: "1h",
			Since:    since,
			Until:    now,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query current: %v", err)), nil
		}

		// Get baseline buckets
		baselineBuckets, err := ts.QueryTrends(ctx, store.TrendQueryParams{
			Interval: "1h",
			Since:    baselineSince,
			Until:    baselineUntil,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query baseline: %v", err)), nil
		}

		// Aggregate by service
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
			curAvg := cur.sum / float64(max(cur.count, 1))
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
				if abs(movers[j].ChangePct) > abs(movers[i].ChangePct) {
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
		return mcp.NewToolResultText(string(data)), nil
	}
}

// extractMetric gets a specific metric value from a MetricBucket.
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

// computeBaseline calculates baseline time range from comparison type.
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

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
