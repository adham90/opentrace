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

// alertDetailsHandler returns a handler that provides full details for a specific
// alert: triggering watcher, run data, and correlated alerts.
func alertDetailsHandler(as store.AlertStore, ws store.WatcherStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		alertIDStr, _ := args["alert_id"].(string)
		if alertIDStr == "" {
			return mcp.NewToolResultError("alert_id is required"), nil
		}

		alertID, err := uuid.Parse(alertIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid alert_id format"), nil
		}

		includeCorrelated := true
		if v, ok := args["include_correlated"].(bool); ok {
			includeCorrelated = v
		}

		// Fetch the alert.
		alert, err := as.GetByID(ctx, alertID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("alert not found: %v", err)), nil
		}

		resp := map[string]any{
			"alert": map[string]any{
				"id":        alert.ID.String(),
				"severity":  string(alert.Severity),
				"title":     alert.Title,
				"summary":   alert.Summary,
				"created_at": alert.CreatedAt.Format(time.RFC3339),
				"read":       alert.Read,
				"dismissed":  alert.Dismissed,
			},
		}

		// Fetch the watcher config if available.
		if alert.WatcherID != nil && ws != nil {
			watcher, err := ws.GetByID(ctx, *alert.WatcherID)
			if err == nil && watcher != nil {
				watcherInfo := map[string]any{
					"id":          watcher.ID.String(),
					"title":       watcher.Title,
					"type":        string(watcher.WatcherType),
					"environment": watcher.Environment,
					"schedule":    watcher.Schedule,
					"time_range":  watcher.TimeRange,
				}
				if watcher.Description != "" {
					watcherInfo["description"] = watcher.Description
				}
				if watcher.RuleConfig != nil {
					watcherInfo["rule_config"] = watcher.RuleConfig
				}
				resp["watcher"] = watcherInfo
			}
		}

		// Fetch the triggering run if available.
		if alert.RunID != nil && rs != nil {
			run, err := rs.GetByID(ctx, *alert.RunID)
			if err == nil && run != nil {
				runInfo := map[string]any{
					"id":         run.ID.String(),
					"started_at": run.StartedAt.Format(time.RFC3339),
					"status":     run.Status,
					"summary":    run.Summary,
				}
				if run.FinishedAt != nil {
					runInfo["completed_at"] = run.FinishedAt.Format(time.RFC3339)
					runInfo["duration_ms"] = run.FinishedAt.Sub(run.StartedAt).Milliseconds()
				}
				if run.Details != nil {
					runInfo["details"] = json.RawMessage(run.Details)
				}
				resp["triggering_run"] = runInfo
			}
		}

		// Fetch correlated alerts (same time window +/- 5 min).
		if includeCorrelated {
			windowStart := alert.CreatedAt.Add(-5 * time.Minute)
			windowEnd := alert.CreatedAt.Add(5 * time.Minute)

			correlated, err := as.List(ctx, store.ListAlertParams{
				Limit: 20,
			})
			if err == nil {
				var correlatedEntries []map[string]any
				for _, a := range correlated {
					if a.ID == alert.ID {
						continue
					}
					if a.CreatedAt.Before(windowStart) || a.CreatedAt.After(windowEnd) {
						continue
					}
					offset := a.CreatedAt.Sub(alert.CreatedAt).Seconds()
					correlatedEntries = append(correlatedEntries, map[string]any{
						"id":                  a.ID.String(),
						"watcher_title":       a.WatcherTitle,
						"severity":            string(a.Severity),
						"summary":             a.Summary,
						"created_at":          a.CreatedAt.Format(time.RFC3339),
						"time_offset_seconds": offset,
					})
				}
				if len(correlatedEntries) > 0 {
					resp["correlated_alerts"] = correlatedEntries
				}
			}
		}

		// Context: counts for this alert's watcher.
		if alert.WatcherID != nil {
			now := time.Now().UTC()
			alertsLast24h, _ := as.List(ctx, store.ListAlertParams{
				WatcherID: alert.WatcherID,
				Limit:     100,
			})
			count24h := 0
			for _, a := range alertsLast24h {
				if a.CreatedAt.After(now.Add(-24 * time.Hour)) {
					count24h++
				}
			}
			resp["context"] = map[string]any{
				"same_watcher_alerts_24h": count24h,
			}
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
