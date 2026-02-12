package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// watcherRunHistoryHandler returns a handler that shows recent execution history
// for a watcher: run status, duration, summary, errors, and alert rate.
func watcherRunHistoryHandler(ws store.WatcherStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		watcherIDStr, _ := args["watcher_id"].(string)
		if watcherIDStr == "" {
			return mcp.NewToolResultError("watcher_id is required"), nil
		}

		watcherID, err := uuid.Parse(watcherIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid watcher_id format"), nil
		}

		// Fetch watcher metadata.
		w, err := ws.GetByID(ctx, watcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("watcher not found: %v", err)), nil
		}

		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
			if limit > 100 {
				limit = 100
			}
		}

		statusFilter := ""
		if v, ok := args["status_filter"].(string); ok && v != "" && v != "all" {
			statusFilter = v
		}

		// Fetch runs with optional filter.
		runs, err := rs.ListWithFilter(ctx, watcherID, limit, statusFilter)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list runs: %v", err)), nil
		}

		// Build run entries.
		type runEntry struct {
			ID          string          `json:"id"`
			StartedAt   string          `json:"started_at"`
			CompletedAt *string         `json:"completed_at,omitempty"`
			DurationMS  *int64          `json:"duration_ms,omitempty"`
			Status      string          `json:"status"`
			HasAlert    bool            `json:"has_alert"`
			Summary     *string         `json:"summary,omitempty"`
			Details     json.RawMessage `json:"details,omitempty"`
			Error       *string         `json:"error,omitempty"`
		}

		entries := make([]runEntry, 0, len(runs))
		for _, r := range runs {
			e := runEntry{
				ID:        r.ID.String(),
				StartedAt: r.StartedAt.Format(time.RFC3339),
				Status:    r.Status,
				HasAlert:  r.HasAlert,
				Summary:   r.Summary,
				Error:     r.Error,
			}
			if r.Details != nil {
				e.Details = r.Details
			}
			if r.FinishedAt != nil {
				s := r.FinishedAt.Format(time.RFC3339)
				e.CompletedAt = &s
				ms := r.FinishedAt.Sub(r.StartedAt).Milliseconds()
				e.DurationMS = &ms
			}
			entries = append(entries, e)
		}

		// Compute stats from last 24h.
		now := time.Now().UTC()
		since := now.Add(-24 * time.Hour)
		totalRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &watcherID})
		completedRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &watcherID, Status: "completed"})
		failedRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &watcherID, Status: "error"})

		// Count alerted runs from the fetched data (approximate).
		alertedCount := 0
		for _, r := range runs {
			if r.HasAlert && r.Status == "completed" {
				alertedCount++
			}
		}

		var alertRatePct float64
		if completedRuns > 0 {
			// Use approximate from fetched runs.
			alertRatePct = float64(alertedCount) / float64(len(entries)) * 100
		}

		stats := map[string]any{
			"total_runs_24h":   totalRuns,
			"completed":        completedRuns,
			"failed":           failedRuns,
			"alert_rate_pct":   alertRatePct,
			"period_covered":   "last 24 hours",
		}

		watcherInfo := map[string]any{
			"id":       w.ID.String(),
			"title":    w.Title,
			"type":     string(w.WatcherType),
			"status":   string(w.Status),
			"schedule": w.Schedule,
		}
		if w.TimeRange != "" {
			watcherInfo["time_range"] = w.TimeRange
		}

		// Compute trend analysis from fetched runs.
		trendAnalysis := computeRunTrend(runs)

		resp := map[string]any{
			"watcher":        watcherInfo,
			"runs":           entries,
			"stats":          stats,
			"trend_analysis": trendAnalysis,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// computeRunTrend analyzes fetched runs and returns trend statistics.
func computeRunTrend(runs []store.WatcherRun) map[string]any {
	if len(runs) == 0 {
		return map[string]any{
			"trend":            "no_data",
			"success_rate_pct": 0.0,
			"alert_rate_pct":   0.0,
		}
	}

	var completed, alerted, errors, consecutiveClean, consecutiveErrors int
	var totalDurationMS int64
	var durationCount int

	for _, r := range runs {
		if r.Status == "completed" {
			completed++
			if r.HasAlert {
				alerted++
			}
		}
		if r.Status == "error" || r.Status == "failed" {
			errors++
		}
		if r.FinishedAt != nil {
			totalDurationMS += r.FinishedAt.Sub(r.StartedAt).Milliseconds()
			durationCount++
		}
	}

	// Count consecutive clean/error from most recent.
	for _, r := range runs {
		if r.Status == "completed" && !r.HasAlert {
			consecutiveClean++
		} else {
			break
		}
	}
	for _, r := range runs {
		if r.Status == "error" || r.Status == "failed" {
			consecutiveErrors++
		} else {
			break
		}
	}

	var successRate, alertRate float64
	if len(runs) > 0 {
		successRate = float64(completed) / float64(len(runs)) * 100
	}
	if completed > 0 {
		alertRate = float64(alerted) / float64(completed) * 100
	}

	var avgDurationMS int64
	if durationCount > 0 {
		avgDurationMS = totalDurationMS / int64(durationCount)
	}

	// Determine trend: compare first-half vs second-half alert rates.
	trend := "stable"
	if len(runs) >= 4 {
		mid := len(runs) / 2
		var firstHalfAlerts, secondHalfAlerts int
		for _, r := range runs[:mid] {
			if r.HasAlert {
				firstHalfAlerts++
			}
		}
		for _, r := range runs[mid:] {
			if r.HasAlert {
				secondHalfAlerts++
			}
		}
		// runs[0] is most recent, so first half = recent
		if firstHalfAlerts > secondHalfAlerts*2 && secondHalfAlerts > 0 {
			trend = "degrading"
		} else if secondHalfAlerts > firstHalfAlerts*2 && firstHalfAlerts > 0 {
			trend = "improving"
		}
	}

	return map[string]any{
		"success_rate_pct":  successRate,
		"alert_rate_pct":    alertRate,
		"avg_duration_ms":   avgDurationMS,
		"trend":             trend,
		"consecutive_clean": consecutiveClean,
		"consecutive_errors": consecutiveErrors,
	}
}
