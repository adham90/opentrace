package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func HandleHeatmap(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	metricField := ArgStringDefault(args, "metric", "request_count")

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

	return JSONResult(resp)
}

func HandleTrends(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	metric := ArgStringDefault(args, "metric", "request_volume")
	interval := ArgStringDefault(args, "interval", "1h")
	sinceStr := ArgStringDefault(args, "since", "24h")
	service := ArgString(args, "service")
	endpoint := ArgString(args, "endpoint")
	environment := ArgString(args, "environment")
	compareTo := ArgString(args, "compare_to")

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

	return JSONResult(resp,
		Suggest("analytics", "Find which endpoints changed the most", map[string]any{
			"action": "movers",
			"metric": metric, "since": sinceStr,
		}),
		Suggest("logs", "Drill into request-level performance", map[string]any{
			"action":     "performance",
			"time_range": sinceStr,
		}),
	)
}

func HandleMovers(ctx context.Context, d AnalyticsDeps, args map[string]any) (*CallToolResult, error) {
	metric := ArgStringDefault(args, "metric", "p95_response")
	sinceStr := ArgStringDefault(args, "since", "24h")
	baseline := ArgStringDefault(args, "baseline", "previous_period")
	limit := ArgInt(args, "limit", 10, 100)

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

	return JSONResult(resp)
}
