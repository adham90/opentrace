package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// watcherHealthTool returns the tool definition for watcher_health_summary.
func watcherHealthTool() mcp.Tool {
	return mcp.NewTool("watcher_health_summary",
		mcp.WithDescription("Fleet-wide watcher overview: active/paused/error counts, stale watchers, noisiest watchers, most failing, adaptive states, and overall alert volume. Use instead of calling list_watchers + watcher_run_history for each one."),
	)
}

// watcherHealthHandler returns a handler that shows fleet-wide watcher health.
func watcherHealthHandler(ws store.WatcherStore, as store.AlertStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		watchers, err := ws.List(ctx, store.ListWatcherParams{})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list watchers: %v", err)), nil
		}

		now := time.Now().UTC()
		since24h := now.Add(-24 * time.Hour)

		// Count by status.
		statusCounts := map[string]int{"active": 0, "paused": 0, "error": 0}
		adaptiveBreakdown := make(map[string]int)
		var staleWatchers []map[string]any
		type watcherAlertInfo struct {
			ID         string
			Title      string
			AlertCount int
			Errors     int
		}
		var watcherInfos []watcherAlertInfo

		for _, w := range watchers {
			statusCounts[string(w.Status)]++

			if w.AdaptiveConfig != nil && w.AdaptiveConfig.Enabled {
				adaptiveBreakdown[string(w.AdaptiveState)]++
			}

			// Detect stale watchers.
			if w.Status == store.WatcherActive {
				if w.LastRunAt == nil || w.LastRunAt.Before(since24h) {
					staleWatchers = append(staleWatchers, map[string]any{
						"id":          w.ID.String(),
						"title":       w.Title,
						"last_run_at": w.LastRunAt,
					})
				}
			}

			// Get per-watcher alert stats.
			stats, _ := as.WatcherAlertStats(ctx, w.ID, since24h)
			alertCount := 0
			if stats != nil {
				alertCount = stats.TotalAlerts
			}
			watcherInfos = append(watcherInfos, watcherAlertInfo{
				ID:         w.ID.String(),
				Title:      w.Title,
				AlertCount: alertCount,
				Errors:     w.ConsecutiveErrors,
			})
		}

		// Sort for noisiest and most failing.
		sort.Slice(watcherInfos, func(i, j int) bool {
			return watcherInfos[i].AlertCount > watcherInfos[j].AlertCount
		})
		noisiest := make([]map[string]any, 0)
		var topNoisiestTitle string
		var topNoisiestCount int
		for i := 0; i < len(watcherInfos) && i < 5; i++ {
			if watcherInfos[i].AlertCount > 0 {
				noisiest = append(noisiest, map[string]any{
					"id":          watcherInfos[i].ID,
					"title":       watcherInfos[i].Title,
					"alert_count": watcherInfos[i].AlertCount,
				})
				if i == 0 {
					topNoisiestTitle = watcherInfos[i].Title
					topNoisiestCount = watcherInfos[i].AlertCount
				}
			}
		}

		sort.Slice(watcherInfos, func(i, j int) bool {
			return watcherInfos[i].Errors > watcherInfos[j].Errors
		})
		mostFailing := make([]map[string]any, 0)
		for i := 0; i < len(watcherInfos) && i < 5; i++ {
			if watcherInfos[i].Errors > 0 {
				mostFailing = append(mostFailing, map[string]any{
					"id":                 watcherInfos[i].ID,
					"title":              watcherInfos[i].Title,
					"consecutive_errors": watcherInfos[i].Errors,
				})
			}
		}

		// Overall alert volume.
		totalAlerts := 0
		for _, wi := range watcherInfos {
			totalAlerts += wi.AlertCount
		}

		// Build recommendations.
		var recommendations []string
		if len(staleWatchers) > 0 {
			recommendations = append(recommendations, fmt.Sprintf("%d stale watcher(s) haven't run in 24h — investigate or pause them", len(staleWatchers)))
		}
		if statusCounts["error"] > 0 {
			recommendations = append(recommendations, fmt.Sprintf("%d watcher(s) in error state — check their configuration", statusCounts["error"]))
		}
		if topNoisiestCount > 10 {
			recommendations = append(recommendations, fmt.Sprintf("Noisiest watcher '%s' fired %d times in 24h — consider tuning", topNoisiestTitle, topNoisiestCount))
		}
		if len(recommendations) == 0 {
			recommendations = append(recommendations, "Fleet looks healthy — no urgent issues")
		}

		resp := map[string]any{
			"fleet_status": map[string]any{
				"total":  len(watchers),
				"active": statusCounts["active"],
				"paused": statusCounts["paused"],
				"error":  statusCounts["error"],
			},
			"alert_volume_24h":   totalAlerts,
			"stale_watchers":     staleWatchers,
			"noisiest":           noisiest,
			"most_failing":       mostFailing,
			"adaptive_breakdown": adaptiveBreakdown,
			"recommendations":    recommendations,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
