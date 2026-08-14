package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// collectErrors summarizes unresolved errors that were active within the
// window. The window is applied by the query (ActiveSince) rather than only
// being used to label results: a diagnose over the last hour that lists errors
// last seen a week ago is describing a different incident than the one being
// diagnosed.
func collectErrors(ctx context.Context, d OverviewDeps, service, env string, since time.Time) map[string]any {
	params := store.ListErrorGroupParams{
		Status:      store.ErrorGroupUnresolved,
		Environment: env,
		ActiveSince: &since,
		SortBy:      "occurrence_count",
		Limit:       5,
	}
	if service != "" {
		params.Service = service
	}

	groups, err := d.ErrorGroupStore.List(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	totalUnresolved, _ := d.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved, env)

	newCount := 0
	topErrors := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		if g.FirstSeenAt.After(since) {
			newCount++
		}
		entry := map[string]any{
			"fingerprint":      g.Fingerprint,
			"exception_class":  g.ExceptionClass,
			"message":          Truncate(g.Message, 100),
			"occurrence_count": g.OccurrenceCount,
			"last_seen_at":     g.LastSeenAt.Format(time.RFC3339),
		}
		if g.Environment != "" {
			entry["environment"] = g.Environment
		}
		topErrors = append(topErrors, entry)
	}

	return map[string]any{
		"total_unresolved": totalUnresolved,
		"active_in_window": len(groups),
		"new_fingerprints": newCount,
		"top_errors":       topErrors,
	}
}

// collectLogVolume counts logs in the window for one environment. The env is not
// optional bookkeeping: this count sat next to an env-scoped error summary while
// itself aggregating every env, so diagnose could report 19 errors and then have
// every error-level drill-down come back empty — the two numbers described
// different systems.
func collectLogVolume(ctx context.Context, d OverviewDeps, service, env string, since, until time.Time) map[string]any {
	params := store.LogCountParams{Since: since, Until: until, Environment: env}
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

// Performance scan bounds. RequestSummarySearchParams has no Service field, so
// a service filter cannot be pushed into the query and has to be applied in Go.
// It therefore has to be applied to a scan wide enough to contain the service's
// rows: filtering AFTER a 5-row limit meant a service-scoped diagnose reported
// total_requests:0 whenever the five slowest requests overall belonged to other
// services.
const (
	perfScanLimit       = 500
	perfSlowestReported = 5
)

func collectPerformance(ctx context.Context, d OverviewDeps, service, env string, since, until time.Time) map[string]any {
	limit := perfSlowestReported
	if service != "" {
		limit = perfScanLimit
	}
	params := store.RequestSummarySearchParams{
		Start:       &since,
		End:         &until,
		Environment: env,
		SortBy:      "duration_ms",
		Limit:       limit,
	}

	results, err := d.LogStore.SearchRequestSummaries(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	if len(results) == 0 {
		return map[string]any{"total_requests": 0}
	}

	scanTruncated := service != "" && len(results) >= perfScanLimit

	filtered := make([]store.RequestSummaryResult, 0, len(results))
	for _, r := range results {
		if service == "" || r.Service == service {
			filtered = append(filtered, r)
		}
	}

	var totalDuration float64
	var totalSQL int
	nPlusOne := 0
	slowest := make([]map[string]any, 0, min(perfSlowestReported, len(filtered)))

	for _, r := range filtered {
		totalDuration += r.DurationMs
		totalSQL += r.SQLCount
		if r.NPlusOne {
			nPlusOne++
		}
	}

	for i, r := range filtered {
		if i >= perfSlowestReported {
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

	out := map[string]any{
		"total_requests":    len(filtered),
		"avg_duration_ms":   fmt.Sprintf("%.1f", avgDuration),
		"avg_sql_count":     fmt.Sprintf("%.1f", avgSQL),
		"n_plus_one_count":  nPlusOne,
		"slowest_endpoints": slowest,
	}
	if scanTruncated {
		out["coverage_note"] = fmt.Sprintf(
			"counted from the %d slowest requests in the window (the store cannot filter by service), so total_requests is a lower bound", perfScanLimit)
	}
	return out
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
			"id":         w.ID,
			"conditions": store.ConditionsSummary(w.ConditionsJSON),
			"urgency":    string(w.Urgency),
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
