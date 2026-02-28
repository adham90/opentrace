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

// systemOverviewTool returns the tool definition for system_overview.
func systemOverviewTool() mcp.Tool {
	return mcp.NewTool("system_overview",
		mcp.WithDescription("Get a high-level dashboard of system health: unresolved error groups, active watch alerts, healthcheck status, log error rates, connector and server status. Use proactively at the start of a session to understand the current state of the system."),
	)
}

// overviewDeps holds the stores needed by both overview and triage handlers.
type overviewDeps struct {
	logStore         store.LogStore
	dsStore          store.DataSourceStore
	serverStore      store.ServerStore
	errorGroupStore  store.ErrorGroupStore
	watchStore       store.WatchStore
	healthCheckStore store.HealthCheckStore
}

// systemOverviewHandler returns a handler that builds a system overview.
func systemOverviewHandler(d overviewDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		overview := map[string]any{}

		// Logs (last hour)
		if d.logStore != nil {
			now := time.Now()
			counts, err := d.logStore.CountByLevel(ctx, store.LogCountParams{
				Since: now.Add(-1 * time.Hour),
				Until: now,
			})
			if err == nil {
				total := 0
				errCount := 0
				for level, count := range counts {
					total += count
					if level == "ERROR" || level == "error" || level == "fatal" || level == "FATAL" {
						errCount += count
					}
				}
				overview["logs"] = map[string]int{
					"last_hour":        total,
					"errors_last_hour": errCount,
				}
			}
		}

		// Unresolved error groups
		if d.errorGroupStore != nil {
			unresolvedCount, err := d.errorGroupStore.Count(ctx, store.ErrorGroupUnresolved)
			if err == nil && unresolvedCount > 0 {
				overview["error_groups"] = map[string]any{
					"unresolved": unresolvedCount,
				}
			}
		}

		// Active watch alerts
		if d.watchStore != nil {
			pendingCount, err := d.watchStore.CountPendingAlerts(ctx)
			if err == nil && pendingCount > 0 {
				overview["watch_alerts"] = map[string]any{
					"pending": pendingCount,
				}
			}
		}

		// Health checks
		if d.healthCheckStore != nil {
			summaries, err := d.healthCheckStore.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
			if err == nil && len(summaries) > 0 {
				total := len(summaries)
				down := 0
				degraded := 0
				for _, s := range summaries {
					switch store.HealthCheckStatus(s.CurrentStatus) {
					case store.HealthCheckDown:
						down++
					case store.HealthCheckDegraded:
						degraded++
					}
				}
				hc := map[string]int{"total": total, "down": down}
				if degraded > 0 {
					hc["degraded"] = degraded
				}
				overview["healthchecks"] = hc
			}
		}

		// Connectors
		if d.dsStore != nil {
			connectors, err := d.dsStore.List(ctx, store.ListDataSourceParams{})
			if err == nil {
				cStats := map[string]int{"total": len(connectors), "connected": 0, "error": 0}
				for _, c := range connectors {
					switch c.Status {
					case store.StatusConnected:
						cStats["connected"]++
					case store.StatusError:
						cStats["error"]++
					}
				}
				overview["connectors"] = cStats
			}
		}

		// Servers
		if d.serverStore != nil {
			servers, err := d.serverStore.List(ctx, store.ListServerParams{})
			if err == nil {
				sStats := map[string]int{"total": len(servers), "online": 0, "offline": 0}
				for _, srv := range servers {
					switch srv.Status {
					case store.ServerOnline:
						sStats["online"]++
					case store.ServerOffline:
						sStats["offline"]++
					}
				}
				overview["servers"] = sStats
			}
		}

		// Suggested next tools based on findings.
		var suggestions []ToolSuggestion
		if eg, ok := overview["error_groups"].(map[string]any); ok {
			if u, _ := eg["unresolved"].(int); u > 0 {
				suggestions = append(suggestions, suggest("error_groups", fmt.Sprintf("%d unresolved errors", u), map[string]any{"status": "unresolved"}))
			}
		}
		if logs, ok := overview["logs"].(map[string]int); ok {
			if logs["errors_last_hour"] > 0 {
				suggestions = append(suggestions, suggest("log_summary", "Investigate errors in last hour", nil))
			}
		}
		if hc, ok := overview["healthchecks"].(map[string]int); ok {
			if hc["down"] > 0 {
				suggestions = append(suggestions, suggest("uptime_status", fmt.Sprintf("%d endpoints down", hc["down"]), nil))
			}
		}
		suggestions = append(suggestions, suggest("diagnose", "Deep dive into a specific service", nil))
		withSuggestions(overview, suggestions...)

		data, err := json.Marshal(overview)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal overview: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// triageAlertsTool returns the tool definition for triage_alerts.
func triageAlertsTool() mcp.Tool {
	return mcp.NewTool("triage_alerts",
		mcp.WithDescription("Get a prioritized list of items needing attention: unresolved error groups, active watch alerts, down healthchecks, error connectors, offline servers. Sorted by severity then recency. Use when the user asks 'what needs attention?' or 'what's broken?'."),
	)
}

// triageItem represents a single triage inbox entry.
type triageEntry struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Time     string `json:"time"`
	ID       string `json:"id"`
}

// triageAlertsHandler returns a handler that builds the triage inbox.
func triageAlertsHandler(d overviewDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var items []triageEntry

		// Unresolved error groups (highest priority)
		if d.errorGroupStore != nil {
			groups, err := d.errorGroupStore.List(ctx, store.ListErrorGroupParams{
				Status: store.ErrorGroupUnresolved,
				SortBy: "occurrence_count",
				Limit:  10,
			})
			if err == nil {
				for _, eg := range groups {
					msg := eg.Message
					if len(msg) > 80 {
						msg = msg[:80] + "..."
					}
					title := msg
					if eg.ExceptionClass != "" {
						title = eg.ExceptionClass + ": " + msg
					}
					items = append(items, triageEntry{
						Type:     "error_group",
						Severity: "critical",
						Title:    title,
						Detail:   fmt.Sprintf("%d occurrences, last seen %s", eg.OccurrenceCount, eg.LastSeenAt.Format(time.RFC3339)),
						Time:     eg.LastSeenAt.Format(time.RFC3339),
						ID:       eg.Fingerprint,
					})
				}
			}
		}

		// Active watch alerts
		if d.watchStore != nil {
			alerts, err := d.watchStore.ListAlerts(ctx, "", "pending", 10)
			if err == nil {
				for _, a := range alerts {
					items = append(items, triageEntry{
						Type:     "watch_alert",
						Severity: "warning",
						Title:    a.Summary,
						Detail:   fmt.Sprintf("%s: %.2f (threshold: %.2f)", a.TriggerMetric, a.TriggerValue, a.ThresholdValue),
						Time:     a.CreatedAt.Format(time.RFC3339),
						ID:       a.ID,
					})
				}
			}
		}

		// Down healthchecks
		if d.healthCheckStore != nil {
			summaries, err := d.healthCheckStore.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
			if err == nil {
				for _, s := range summaries {
					if store.HealthCheckStatus(s.CurrentStatus) == store.HealthCheckDown {
						items = append(items, triageEntry{
							Type:     "healthcheck",
							Severity: "critical",
							Title:    fmt.Sprintf("Endpoint '%s' is DOWN", s.Name),
							Detail:   s.URL,
							Time:     time.Now().Format(time.RFC3339),
							ID:       s.HealthCheckID,
						})
					}
				}
			}
		}

		// Error connectors
		if d.dsStore != nil {
			connectors, err := d.dsStore.List(ctx, store.ListDataSourceParams{})
			if err == nil {
				for _, c := range connectors {
					if c.Status == store.StatusError {
						detail := ""
						if c.StatusMessage != nil {
							detail = *c.StatusMessage
							if len(detail) > 100 {
								detail = detail[:100]
							}
						}
						items = append(items, triageEntry{
							Type:     "connector",
							Severity: "warning",
							Title:    "Connector '" + c.Name + "' error",
							Detail:   detail,
							Time:     c.UpdatedAt.Format(time.RFC3339),
							ID:       c.ID.String(),
						})
					}
				}
			}
		}

		// Offline servers
		if d.serverStore != nil {
			servers, err := d.serverStore.List(ctx, store.ListServerParams{})
			if err == nil {
				for _, srv := range servers {
					if srv.Status == store.ServerOffline {
						detail := "last seen: never"
						if srv.LastSeenAt != nil {
							detail = "last seen " + srv.LastSeenAt.Format(time.RFC3339)
						}
						items = append(items, triageEntry{
							Type:     "server",
							Severity: "info",
							Title:    "Server '" + srv.Hostname + "' offline",
							Detail:   detail,
							Time:     srv.CreatedAt.Format(time.RFC3339),
							ID:       srv.ID.String(),
						})
					}
				}
			}
		}

		// Link triggered entities to investigation session from triage.
		if recurrenceDetector != nil && sessionTracker != nil {
			if sid := sessionTracker.CurrentSessionID(); sid != "" {
				for _, item := range items {
					switch item.Type {
					case "watch_alert":
						// item.ID is alert ID; look up the watch ID from alert.
						if d.watchStore != nil {
							if alert, err := d.watchStore.GetAlert(ctx, item.ID); err == nil {
								recurrenceDetector.LinkTriggeredWatcher(ctx, sid, alert.WatchID)
							}
						}
					case "healthcheck":
						recurrenceDetector.LinkTriggeredHealthcheck(ctx, sid, item.ID)
					}
					break // only link the top item
				}
			}
		}

		// Sort by severity then time
		sort.Slice(items, func(i, j int) bool {
			si, sj := sevOrder(items[i].Severity), sevOrder(items[j].Severity)
			if si != sj {
				return si < sj
			}
			return items[i].Time > items[j].Time
		})

		if len(items) > 20 {
			items = items[:20]
		}

		if len(items) == 0 {
			return mcp.NewToolResultText("Nothing needs attention. System looks healthy."), nil
		}

		resp := map[string]any{
			"count": len(items),
			"items": items,
		}

		// Suggest investigating the top item.
		var suggestions []ToolSuggestion
		top := items[0]
		switch top.Type {
		case "error_group":
			suggestions = append(suggestions, suggest("error_detail", "Investigate top error", map[string]any{
				"fingerprint": top.ID,
			}))
		case "watch_alert":
			suggestions = append(suggestions, suggest("investigate", "Investigate top alert", map[string]any{
				"alert_id": top.ID,
			}))
		case "healthcheck":
			suggestions = append(suggestions, suggest("uptime_status", "Check endpoint uptime details", nil))
		}
		if len(items) > 1 {
			suggestions = append(suggestions, suggest("diagnose", "Full system investigation", nil))
		}
		withSuggestions(resp, suggestions...)

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal triage: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func sevOrder(sev string) int {
	switch sev {
	case "critical":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}
