package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/store"
)

// anomalyDetectHandler returns a handler that detects statistical anomalies.
func anomalyDetectHandler(logStore store.LogStore, alertStore store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		metric, _ := args["metric"].(string)
		if metric == "" {
			return mcp.NewToolResultError("metric is required. Options: error_rate, log_volume, alert_count"), nil
		}

		windowStr := "1h"
		if v, ok := args["window"].(string); ok && v != "" {
			windowStr = v
		}

		service, _ := args["service"].(string)

		window, err := parseDurationLoose(windowStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid window: %v", err)), nil
		}

		now := time.Now().UTC()
		currentStart := now.Add(-window)

		// Collect samples from 7 previous periods for baseline.
		const numSamples = 7
		samples := make([]float64, 0, numSamples)

		for i := 1; i <= numSamples; i++ {
			periodEnd := now.Add(-time.Duration(i) * window)
			periodStart := periodEnd.Add(-window)

			val, err := measureMetric(ctx, metric, periodStart, periodEnd, service, logStore, alertStore)
			if err != nil {
				continue
			}
			samples = append(samples, val)
		}

		currentVal, err := measureMetric(ctx, metric, currentStart, now, service, logStore, alertStore)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to measure current value: %v", err)), nil
		}

		// Calculate stats.
		mean, stddev := meanStdDev(samples)
		var zScore float64
		if stddev > 0 {
			zScore = (currentVal - mean) / stddev
		}

		isAnomaly := math.Abs(zScore) > 2.0
		direction := "normal"
		if zScore > 2.0 {
			direction = "above_normal"
		} else if zScore < -2.0 {
			direction = "below_normal"
		}

		resp := map[string]any{
			"metric":        metric,
			"window":        windowStr,
			"current_value": currentVal,
			"baseline": map[string]any{
				"mean":       fmt.Sprintf("%.2f", mean),
				"stddev":     fmt.Sprintf("%.2f", stddev),
				"samples":    len(samples),
				"sample_values": samples,
			},
			"z_score":    fmt.Sprintf("%.2f", zScore),
			"is_anomaly": isAnomaly,
			"direction":  direction,
		}

		if isAnomaly {
			changePct := 0.0
			if mean > 0 {
				changePct = 100.0 * (currentVal - mean) / mean
			}
			resp["change_pct"] = fmt.Sprintf("%.1f%%", changePct)
			resp["severity"] = anomalySeverity(math.Abs(zScore))
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// measureMetric gets the numeric value for a metric in a time window.
func measureMetric(ctx context.Context, metric string, start, end time.Time, service string, ls store.LogStore, as store.AlertStore) (float64, error) {
	switch metric {
	case "error_rate":
		if ls == nil {
			return 0, fmt.Errorf("log store not available")
		}
		counts, err := ls.CountByLevel(ctx, store.LogCountParams{
			Since: start, Until: end, Service: service, Level: "error",
		})
		if err != nil {
			return 0, err
		}
		total := 0
		for _, c := range counts {
			total += c
		}
		return float64(total), nil

	case "log_volume":
		if ls == nil {
			return 0, fmt.Errorf("log store not available")
		}
		counts, err := ls.CountByLevel(ctx, store.LogCountParams{
			Since: start, Until: end, Service: service,
		})
		if err != nil {
			return 0, err
		}
		total := 0
		for _, c := range counts {
			total += c
		}
		return float64(total), nil

	case "alert_count":
		if as == nil {
			return 0, fmt.Errorf("alert store not available")
		}
		counts, err := as.CountBySeverity(ctx, start, end)
		if err != nil {
			return 0, err
		}
		total := 0
		for _, c := range counts {
			total += c
		}
		return float64(total), nil

	default:
		return 0, fmt.Errorf("unknown metric: %s", metric)
	}
}

// meanStdDev calculates mean and standard deviation of a sample.
func meanStdDev(samples []float64) (float64, float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range samples {
		sum += v
	}
	mean := sum / float64(len(samples))

	if len(samples) < 2 {
		return mean, 0
	}

	variance := 0.0
	for _, v := range samples {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(samples) - 1)
	return mean, math.Sqrt(variance)
}

// anomalySeverity maps z-score magnitude to severity.
func anomalySeverity(absZ float64) string {
	switch {
	case absZ > 4:
		return "critical"
	case absZ > 3:
		return "high"
	case absZ > 2:
		return "medium"
	default:
		return "low"
	}
}

// parseDurationLoose parses duration strings like "1h", "15m", "6h", "24h", "7d".
func parseDurationLoose(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration: %s", s)
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	var num float64
	_, err := fmt.Sscanf(numStr, "%f", &num)
	if err != nil {
		return 0, fmt.Errorf("invalid duration number: %s", s)
	}
	switch unit {
	case 'm':
		return time.Duration(num) * time.Minute, nil
	case 'h':
		return time.Duration(num) * time.Hour, nil
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown duration unit: %c (use m/h/d)", unit)
	}
}
