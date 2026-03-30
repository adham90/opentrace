package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func collectErrors(ctx context.Context, d OverviewDeps, service string, since time.Time) map[string]any {
	params := store.ListErrorGroupParams{
		Status: store.ErrorGroupUnresolved,
		SortBy: "occurrence_count",
		Limit:  5,
	}
	if service != "" {
		params.Service = service
	}

	groups, err := d.ErrorGroupStore.List(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	totalUnresolved, _ := d.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved)

	newCount := 0
	topErrors := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		if g.FirstSeenAt.After(since) {
			newCount++
		}
		topErrors = append(topErrors, map[string]any{
			"fingerprint":      g.Fingerprint,
			"exception_class":  g.ExceptionClass,
			"message":          Truncate(g.Message, 100),
			"occurrence_count": g.OccurrenceCount,
			"last_seen_at":     g.LastSeenAt.Format(time.RFC3339),
		})
	}

	return map[string]any{
		"total_unresolved": totalUnresolved,
		"new_fingerprints": newCount,
		"top_errors":       topErrors,
	}
}

func collectLogVolume(ctx context.Context, d OverviewDeps, service string, since, until time.Time) map[string]any {
	params := store.LogCountParams{Since: since, Until: until}
	if service != "" {
		params.Service = service
	}

	byLevel, err := d.LogStore.CountByLevel(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	total := 0
	for _, c := range byLevel {
		total += c
	}
	errorCount := byLevel["error"] + byLevel["fatal"]

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

func collectPerformance(ctx context.Context, d OverviewDeps, service string, since, until time.Time) map[string]any {
	params := store.RequestSummarySearchParams{
		Start:  &since,
		End:    &until,
		SortBy: "duration_ms",
		Limit:  5,
	}

	results, err := d.LogStore.SearchRequestSummaries(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	if len(results) == 0 {
		return map[string]any{"total_requests": 0}
	}

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

func collectWatchAlerts(ctx context.Context, d OverviewDeps, service string) map[string]any {
	params := store.ListWatchParams{Status: store.WatchStatusTriggered}
	if service != "" {
		params.Service = service
	}

	triggered, err := d.WatchStore.List(ctx, params)
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

	pendingCount, _ := d.WatchStore.CountPendingAlerts(ctx)

	return map[string]any{
		"triggered_count": len(triggered),
		"pending_alerts":  pendingCount,
		"alerts":          alerts,
	}
}

func collectHealthChecks(ctx context.Context, d OverviewDeps, since time.Time) map[string]any {
	summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, since)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	downCount := 0
	degradedCount := 0
	checks := make([]map[string]any, 0, len(summaries))
	for _, s := range summaries {
		entry := map[string]any{
			"name":            s.Name,
			"url":             s.URL,
			"status":          s.CurrentStatus,
			"uptime_pct":      fmt.Sprintf("%.1f", s.UptimePct),
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
		"total":     len(summaries),
		"down":      downCount,
		"degraded":  degradedCount,
		"endpoints": checks,
	}
}
