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

// watcherEffectivenessTool returns the tool definition for watcher_effectiveness.
func watcherEffectivenessTool() mcp.Tool {
	return mcp.NewTool("watcher_effectiveness",
		mcp.WithDescription("Assess a watcher's effectiveness: signal-to-noise ratio, false positive rate, alert trends, run success rate, and human feedback from dismiss reasons. Use to decide if a watcher needs tuning, pausing, or deletion."),
		mcp.WithString("watcher_id", mcp.Required(), mcp.Description("Watcher UUID")),
		mcp.WithString("period", mcp.Description("Analysis period: '7d' (default), '24h', '30d'")),
	)
}

// watcherEffectivenessHandler returns a handler that computes watcher effectiveness.
func watcherEffectivenessHandler(ws store.WatcherStore, as store.AlertStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
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

		w, err := ws.GetByID(ctx, watcherID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("watcher not found: %v", err)), nil
		}

		// Parse period.
		now := time.Now().UTC()
		period := "7d"
		if v, ok := args["period"].(string); ok && v != "" {
			period = v
		}
		var since time.Time
		switch period {
		case "24h":
			since = now.Add(-24 * time.Hour)
		case "30d":
			since = now.Add(-30 * 24 * time.Hour)
		default:
			period = "7d"
			since = now.Add(-7 * 24 * time.Hour)
		}

		// Get alert stats.
		eff, err := as.WatcherAlertStats(ctx, watcherID, since)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get alert stats: %v", err)), nil
		}
		eff.WatcherTitle = w.Title
		eff.Period = period

		// Get run counts.
		totalRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &watcherID})
		completedRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &watcherID, Status: "completed"})
		errorRuns, _ := rs.CountRuns(ctx, store.CountRunParams{Since: since, Until: now, WatcherID: &watcherID, Status: "error"})
		eff.TotalRuns = totalRuns
		eff.CompletedRuns = completedRuns
		eff.ErrorRuns = errorRuns

		if completedRuns > 0 {
			eff.AlertRatePct = float64(eff.TotalAlerts) / float64(completedRuns) * 100
		}

		// Compute avg duration from recent runs.
		runs, _ := rs.List(ctx, watcherID, 20)
		var totalDuration int64
		var durationCount int
		for _, r := range runs {
			if r.FinishedAt != nil {
				totalDuration += r.FinishedAt.Sub(r.StartedAt).Milliseconds()
				durationCount++
			}
		}
		if durationCount > 0 {
			eff.AvgDurationMS = totalDuration / int64(durationCount)
		}

		// Determine trend by comparing first-half vs second-half alert rates.
		midpoint := since.Add(now.Sub(since) / 2)
		firstHalf, _ := as.WatcherAlertStats(ctx, watcherID, since)
		secondHalf, _ := as.WatcherAlertStats(ctx, watcherID, midpoint)
		if firstHalf != nil && secondHalf != nil {
			firstAlerts := firstHalf.TotalAlerts - secondHalf.TotalAlerts
			secondAlerts := secondHalf.TotalAlerts
			if firstAlerts > 0 && secondAlerts > firstAlerts*2 {
				eff.Trend = "degrading"
			} else if firstAlerts > secondAlerts*2 && firstAlerts > 0 {
				eff.Trend = "improving"
			} else {
				eff.Trend = "stable"
			}
		} else {
			eff.Trend = "stable"
		}

		data, err := json.MarshalIndent(eff, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
