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

// disableStaleWatchersTool returns the tool definition for disable_stale_watchers.
func disableStaleWatchersTool() mcp.Tool {
	return mcp.NewTool("disable_stale_watchers",
		mcp.WithDescription("Find and optionally pause watchers that haven't produced alerts in N days. Use dry_run=true (default) to preview which watchers would be paused before taking action. Helps clean up watchers that are no longer providing value."),
		mcp.WithNumber("days", mcp.Description("Number of days without alerts to consider a watcher stale (default: 30)")),
		mcp.WithBoolean("dry_run", mcp.Description("If true (default), only report stale watchers without pausing them. Set to false to actually pause them.")),
	)
}

// disableStaleWatchersHandler returns a handler that finds and pauses stale watchers.
func disableStaleWatchersHandler(ws store.WatcherStore, as store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		// Parse days parameter (float64 from JSON).
		days := 30
		if v, ok := args["days"].(float64); ok && v > 0 {
			days = int(v)
		}

		// Parse dry_run parameter (default: true).
		dryRun := true
		if v, ok := args["dry_run"].(bool); ok {
			dryRun = v
		}

		// List only active watchers.
		watchers, err := ws.List(ctx, store.ListWatcherParams{})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list watchers: %v", err)), nil
		}

		// Filter to active watchers only.
		var activeWatchers []store.Watcher
		for _, w := range watchers {
			if w.Status == store.WatcherActive {
				activeWatchers = append(activeWatchers, w)
			}
		}

		if len(activeWatchers) == 0 {
			return mcp.NewToolResultText("No active watchers found."), nil
		}

		now := time.Now().UTC()
		threshold := now.Add(-time.Duration(days) * 24 * time.Hour)

		type staleWatcher struct {
			ID          string     `json:"id"`
			Title       string     `json:"title"`
			Environment string     `json:"environment,omitempty"`
			LastAlertAt *time.Time `json:"last_alert_at,omitempty"`
			TotalAlerts int        `json:"total_alerts_in_period"`
			Paused      bool       `json:"paused"`
		}

		var staleWatchers []staleWatcher
		var pauseErrors []string

		for _, w := range activeWatchers {
			stats, err := as.WatcherAlertStats(ctx, w.ID, threshold)
			if err != nil {
				// If we can't get stats, skip this watcher.
				continue
			}

			// A watcher is stale if it produced zero alerts in the period
			// and either has no last alert or the last alert is before the threshold.
			isStale := stats.TotalAlerts == 0 && (stats.LastAlertAt == nil || stats.LastAlertAt.Before(threshold))
			if !isStale {
				continue
			}

			entry := staleWatcher{
				ID:          w.ID.String(),
				Title:       w.Title,
				Environment: w.Environment,
				LastAlertAt: stats.LastAlertAt,
				TotalAlerts: stats.TotalAlerts,
			}

			if !dryRun {
				_, err := ws.UpdateStatus(ctx, w.ID, store.WatcherPaused)
				if err != nil {
					pauseErrors = append(pauseErrors, fmt.Sprintf("%s (%s): %v", w.ID, w.Title, err))
					entry.Paused = false
				} else {
					entry.Paused = true
				}
			}

			staleWatchers = append(staleWatchers, entry)
		}

		result := struct {
			DryRun       bool           `json:"dry_run"`
			Days         int            `json:"days_threshold"`
			TotalActive  int            `json:"total_active_watchers"`
			StaleCount   int            `json:"stale_count"`
			StaleDetails []staleWatcher `json:"stale_watchers"`
			Errors       []string       `json:"errors,omitempty"`
		}{
			DryRun:       dryRun,
			Days:         days,
			TotalActive:  len(activeWatchers),
			StaleCount:   len(staleWatchers),
			StaleDetails: staleWatchers,
			Errors:       pauseErrors,
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}

		action := "identified"
		if !dryRun {
			action = "paused"
		}
		return mcp.NewToolResultText(fmt.Sprintf("Found %d stale watchers out of %d active (no alerts in %d days). %s\n%s",
			len(staleWatchers), len(activeWatchers), days, action, string(data))), nil
	}
}
