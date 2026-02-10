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

// monitorRunHistoryHandler returns a handler that shows recent execution history
// for a monitor: run status, duration, summary, errors, and alert rate.
func monitorRunHistoryHandler(ws store.WatcherStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		monitorIDStr, _ := args["monitor_id"].(string)
		if monitorIDStr == "" {
			return mcp.NewToolResultError("monitor_id is required"), nil
		}

		monitorID, err := uuid.Parse(monitorIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid monitor_id format"), nil
		}

		// Fetch monitor metadata.
		monitor, err := ws.GetByID(ctx, monitorID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("monitor not found: %v", err)), nil
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
		runs, err := rs.ListWithFilter(ctx, monitorID, limit, statusFilter)
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
		totalRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &monitorID})
		completedRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &monitorID, Status: "completed"})
		failedRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &monitorID, Status: "error"})

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

		monitorInfo := map[string]any{
			"id":       monitor.ID.String(),
			"title":    monitor.Title,
			"type":     string(monitor.MonitorType),
			"status":   string(monitor.Status),
			"schedule": monitor.Schedule,
		}
		if monitor.TimeRange != "" {
			monitorInfo["time_range"] = monitor.TimeRange
		}

		resp := map[string]any{
			"monitor": monitorInfo,
			"runs":    entries,
			"stats":   stats,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
