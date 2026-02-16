package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// diagnoseDeps holds all stores the diagnose tool can query.
type diagnoseDeps struct {
	logStore         store.LogStore
	errorGroupStore  store.ErrorGroupStore
	healthCheckStore store.HealthCheckStore
	watchStore       store.WatchStore
}

// diagnoseHandler returns an all-in-one investigation tool.
func diagnoseHandler(d diagnoseDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		service, _ := args["service"].(string)

		// Parse timeframe (default 1h)
		timeframe := "1h"
		if v, _ := args["timeframe"].(string); v != "" {
			timeframe = v
		}
		duration, err := parseTimeframe(timeframe)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid timeframe %q: %v", timeframe, err)), nil
		}

		now := time.Now().UTC()
		since := now.Add(-duration)

		// Collect sections in parallel
		var wg sync.WaitGroup
		var mu sync.Mutex
		resp := map[string]any{
			"service":   service,
			"timeframe": timeframe,
			"since":     since.Format(time.RFC3339),
			"until":     now.Format(time.RFC3339),
		}

		// 1. Error summary
		if d.errorGroupStore != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				section := d.collectErrors(ctx, service, since)
				mu.Lock()
				resp["error_summary"] = section
				mu.Unlock()
			}()
		}

		// 2. Log volume
		if d.logStore != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				section := d.collectLogVolume(ctx, service, since, now)
				mu.Lock()
				resp["log_volume"] = section
				mu.Unlock()
			}()
		}

		// 3. Request performance
		if d.logStore != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				section := d.collectPerformance(ctx, service, since, now)
				mu.Lock()
				resp["request_performance"] = section
				mu.Unlock()
			}()
		}

		// 4. Watch alerts
		if d.watchStore != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				section := d.collectWatchAlerts(ctx, service)
				mu.Lock()
				resp["watch_alerts"] = section
				mu.Unlock()
			}()
		}

		// 5. Health check status
		if d.healthCheckStore != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				section := d.collectHealthChecks(ctx, since)
				mu.Lock()
				resp["healthcheck_status"] = section
				mu.Unlock()
			}()
		}

		wg.Wait()

		// Build suggested next tools
		resp["suggested_tools"] = d.buildSuggestions(resp, service)

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func (d *diagnoseDeps) collectErrors(ctx context.Context, service string, since time.Time) map[string]any {
	params := store.ListErrorGroupParams{
		Status: store.ErrorGroupUnresolved,
		SortBy: "occurrence_count",
		Limit:  5,
	}
	if service != "" {
		params.Service = service
	}

	groups, err := d.errorGroupStore.List(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	totalUnresolved, _ := d.errorGroupStore.Count(ctx, store.ErrorGroupUnresolved)

	// Count new fingerprints (first seen after since)
	newCount := 0
	topErrors := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		if g.FirstSeenAt.After(since) {
			newCount++
		}
		topErrors = append(topErrors, map[string]any{
			"fingerprint":      g.Fingerprint,
			"exception_class":  g.ExceptionClass,
			"message":          truncateMsg(g.Message, 100),
			"occurrence_count": g.OccurrenceCount,
			"last_seen_at":     g.LastSeenAt.Format(time.RFC3339),
		})
	}

	return map[string]any{
		"total_unresolved":  totalUnresolved,
		"new_fingerprints":  newCount,
		"top_errors":        topErrors,
	}
}

func (d *diagnoseDeps) collectLogVolume(ctx context.Context, service string, since, until time.Time) map[string]any {
	params := store.LogCountParams{Since: since, Until: until}
	if service != "" {
		params.Service = service
	}

	byLevel, err := d.logStore.CountByLevel(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	total := 0
	for _, c := range byLevel {
		total += c
	}
	errorCount := byLevel["ERROR"] + byLevel["FATAL"]

	var errorRate float64
	if total > 0 {
		errorRate = float64(errorCount) / float64(total) * 100
	}

	trend := "stable"
	if total > 1000 {
		trend = "high"
	}

	return map[string]any{
		"total":          total,
		"by_level":       byLevel,
		"error_count":    errorCount,
		"error_rate_pct": fmt.Sprintf("%.1f", errorRate),
		"trend":          trend,
	}
}

func (d *diagnoseDeps) collectPerformance(ctx context.Context, service string, since, until time.Time) map[string]any {
	params := store.RequestSummarySearchParams{
		Start:  &since,
		End:    &until,
		SortBy: "duration_ms",
		Limit:  5,
	}

	results, err := d.logStore.SearchRequestSummaries(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	if len(results) == 0 {
		return map[string]any{"total_requests": 0}
	}

	// Filter by service if provided (request summaries may not have service filter)
	var filtered []store.RequestSummaryResult
	for _, r := range results {
		if service == "" || r.Service == service {
			filtered = append(filtered, r)
		}
	}

	var totalDuration float64
	var totalSQL int
	nPlusOne := 0
	slowest := make([]map[string]any, 0, min(5, len(filtered)))

	for _, r := range filtered {
		totalDuration += r.DurationMs
		totalSQL += r.SQLCount
		if r.NPlusOne {
			nPlusOne++
		}
	}

	for i, r := range filtered {
		if i >= 5 {
			break
		}
		entry := map[string]any{
			"path":        r.Path,
			"duration_ms": r.DurationMs,
			"sql_count":   r.SQLCount,
		}
		if r.NPlusOne {
			entry["n_plus_one"] = true
		}
		if r.Controller != "" {
			entry["controller"] = r.Controller + "#" + r.Action
		}
		slowest = append(slowest, entry)
	}

	var avgDuration float64
	var avgSQL float64
	if len(filtered) > 0 {
		avgDuration = totalDuration / float64(len(filtered))
		avgSQL = float64(totalSQL) / float64(len(filtered))
	}

	return map[string]any{
		"total_requests":    len(filtered),
		"avg_duration_ms":   fmt.Sprintf("%.1f", avgDuration),
		"avg_sql_count":     fmt.Sprintf("%.1f", avgSQL),
		"n_plus_one_count":  nPlusOne,
		"slowest_endpoints": slowest,
	}
}

func (d *diagnoseDeps) collectWatchAlerts(ctx context.Context, service string) map[string]any {
	params := store.ListWatchParams{Status: store.WatchStatusTriggered}
	if service != "" {
		params.Service = service
	}

	triggered, err := d.watchStore.List(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	alerts := make([]map[string]any, 0, len(triggered))
	for _, w := range triggered {
		entry := map[string]any{
			"id":        w.ID,
			"metric":    string(w.Metric),
			"threshold": w.Threshold,
			"urgency":   string(w.Urgency),
		}
		if w.CurrentValue != nil {
			entry["current_value"] = *w.CurrentValue
		}
		if w.Service != "" {
			entry["service"] = w.Service
		}
		alerts = append(alerts, entry)
	}

	pendingCount, _ := d.watchStore.CountPendingAlerts(ctx)

	return map[string]any{
		"triggered_count": len(triggered),
		"pending_alerts":  pendingCount,
		"alerts":          alerts,
	}
}

func (d *diagnoseDeps) collectHealthChecks(ctx context.Context, since time.Time) map[string]any {
	summaries, err := d.healthCheckStore.UptimeSummaries(ctx, since)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	downCount := 0
	degradedCount := 0
	checks := make([]map[string]any, 0, len(summaries))
	for _, s := range summaries {
		entry := map[string]any{
			"name":           s.Name,
			"url":            s.URL,
			"status":         s.CurrentStatus,
			"uptime_pct":     fmt.Sprintf("%.1f", s.UptimePct),
			"avg_response_ms": fmt.Sprintf("%.0f", s.AvgResponseMs),
		}
		checks = append(checks, entry)
		if s.CurrentStatus == "down" {
			downCount++
		}
		if s.CurrentStatus == "degraded" {
			degradedCount++
		}
	}

	return map[string]any{
		"total":          len(summaries),
		"down":           downCount,
		"degraded":       degradedCount,
		"endpoints":      checks,
	}
}

func (d *diagnoseDeps) buildSuggestions(resp map[string]any, service string) []map[string]any {
	var suggestions []map[string]any

	// If errors exist, suggest error_detail for top error
	if es, ok := resp["error_summary"].(map[string]any); ok {
		if topErrors, ok := es["top_errors"].([]map[string]any); ok && len(topErrors) > 0 {
			if fp, ok := topErrors[0]["fingerprint"].(string); ok && fp != "" {
				suggestions = append(suggestions, map[string]any{
					"tool": "error_detail",
					"args": map[string]any{"fingerprint": fp},
				})
			}
		}
	}

	// If high error rate, suggest log_search for errors
	if lv, ok := resp["log_volume"].(map[string]any); ok {
		if ec, ok := lv["error_count"].(int); ok && ec > 0 {
			args := map[string]any{"level": "error", "limit": float64(10)}
			if service != "" {
				args["service"] = service
			}
			suggestions = append(suggestions, map[string]any{
				"tool": "log_search",
				"args": args,
			})
		}
	}

	// If watches triggered, suggest watch_status
	if wa, ok := resp["watch_alerts"].(map[string]any); ok {
		if tc, ok := wa["triggered_count"].(int); ok && tc > 0 {
			suggestions = append(suggestions, map[string]any{
				"tool": "watch_status",
				"args": map[string]any{"status": "triggered"},
			})
		}
	}

	// If healthchecks down, suggest list_healthchecks
	if hc, ok := resp["healthcheck_status"].(map[string]any); ok {
		if dc, ok := hc["down"].(int); ok && dc > 0 {
			suggestions = append(suggestions, map[string]any{
				"tool": "list_healthchecks",
			})
		}
	}

	return suggestions
}

// parseTimeframe converts strings like "1h", "30m", "24h", "7d" to a duration.
func parseTimeframe(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("too short")
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	var num float64
	if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
		return 0, err
	}
	switch unit {
	case 'm':
		return time.Duration(num) * time.Minute, nil
	case 'h':
		return time.Duration(num) * time.Hour, nil
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %c (use m, h, or d)", unit)
	}
}

func truncateMsg(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

