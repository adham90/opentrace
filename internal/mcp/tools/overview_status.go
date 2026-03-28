package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// --- status action ---

func HandleOverviewStatus(ctx context.Context, d OverviewDeps) (*CallToolResult, error) {
	overview := map[string]any{}

	// Logs (last hour)
	if d.LogStore != nil {
		now := time.Now()
		counts, err := d.LogStore.CountByLevel(ctx, store.LogCountParams{
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
	if d.ErrorGroupStore != nil {
		unresolvedCount, err := d.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved)
		if err == nil && unresolvedCount > 0 {
			overview["error_groups"] = map[string]any{
				"unresolved": unresolvedCount,
			}
		}
	}

	// Active watch alerts
	if d.WatchStore != nil {
		pendingCount, err := d.WatchStore.CountPendingAlerts(ctx)
		if err == nil && pendingCount > 0 {
			overview["watch_alerts"] = map[string]any{
				"pending": pendingCount,
			}
		}
	}

	// Health checks
	if d.HealthCheckStore != nil {
		summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
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
	if d.DSStore != nil {
		connectors, err := d.DSStore.List(ctx, store.ListDataSourceParams{})
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
	if d.ServerStore != nil {
		servers, err := d.ServerStore.List(ctx, store.ListServerParams{})
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
			suggestions = append(suggestions, Suggest("errors", fmt.Sprintf("%d unresolved errors", u), map[string]any{"action": "list", "status": "unresolved"}))
		}
	}
	if logs, ok := overview["logs"].(map[string]int); ok {
		if logs["errors_last_hour"] > 0 {
			suggestions = append(suggestions, Suggest("logs", "Investigate errors in last hour", map[string]any{"action": "summary"}))
		}
	}
	if hc, ok := overview["healthchecks"].(map[string]int); ok {
		if hc["down"] > 0 {
			suggestions = append(suggestions, Suggest("healthchecks", fmt.Sprintf("%d endpoints down", hc["down"]), map[string]any{"action": "uptime"}))
		}
	}
	suggestions = append(suggestions, Suggest("overview", "Deep dive into a specific service", map[string]any{"action": "diagnose"}))

	return JSONResult(overview, suggestions...)
}
