package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// suggestMonitorsHandler returns a handler that analyzes the current system
// state and suggests monitors the user should create.
func suggestMonitorsHandler(ws store.WatcherStore, ls store.LogStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		focus := "all"
		if v, ok := args["focus"].(string); ok && v != "" {
			focus = v
		}

		// Load existing monitors.
		var existingMonitors []store.Watcher
		if ws != nil {
			monitors, err := ws.List(ctx, store.ListWatcherParams{})
			if err == nil {
				existingMonitors = monitors
			}
		}

		// Categorize existing monitors.
		coverage := map[string]int{
			"performance": 0,
			"errors":      0,
			"health":      0,
			"security":    0,
		}
		existingTitles := map[string]bool{}
		for _, m := range existingMonitors {
			existingTitles[m.Title] = true
			cat := categorizeMonitor(m)
			coverage[cat]++
		}

		var suggestions []map[string]any
		var gaps []string

		// Analyze logs for error patterns.
		if (focus == "all" || focus == "errors") && ls != nil {
			now := time.Now().UTC()
			since := now.Add(-time.Hour)

			// Check error rates.
			levels, err := ls.CountByLevel(ctx, store.LogCountParams{Since: since, Until: now})
			if err == nil {
				errorCount := levels["error"] + levels["fatal"]
				total := 0
				for _, c := range levels {
					total += c
				}
				if errorCount > 10 && !hasMonitorLike(existingMonitors, "error") {
					errRate := float64(errorCount) / float64(total) * 100
					suggestions = append(suggestions, map[string]any{
						"priority": "high",
						"category": "errors",
						"title":    "Monitor error rate",
						"reason":   fmt.Sprintf("%.1f%% error rate (%d errors in the last hour) with no error rate monitor", errRate, errorCount),
						"monitor_config": map[string]any{
							"title":        "Error Rate Alert",
							"monitor_type": "rule",
							"rule_config": map[string]any{
								"source":    "logs",
								"filter":    map[string]any{"level": "error"},
								"metric":    "count",
								"operator":  "gt",
								"threshold": 50,
							},
							"severity":   "warning",
							"schedule":   "*/5 * * * *",
							"time_range": "5m",
						},
					})
				}
			}

			// Check for dominant error patterns.
			errorLogs, err := ls.Search(ctx, store.LogSearchParams{
				Level: "error",
				Start: &since,
				End:   &now,
				Limit: 1000,
			})
			if err == nil && len(errorLogs) > 20 {
				patterns := make(map[string]int)
				samples := make(map[string]string)
				for _, l := range errorLogs {
					norm := normalizeMessage(l.Message)
					patterns[norm]++
					if _, ok := samples[norm]; !ok {
						samples[norm] = l.Message
					}
				}
				for pattern, count := range patterns {
					if count > 10 && float64(count)/float64(len(errorLogs)) > 0.2 {
						if !hasMonitorLike(existingMonitors, pattern) {
							suggestions = append(suggestions, map[string]any{
								"priority": "medium",
								"category": "errors",
								"title":    fmt.Sprintf("Monitor '%s' error pattern", truncate(pattern, 50)),
								"reason":   fmt.Sprintf("%d occurrences of this pattern in the last hour (%.0f%% of errors)", count, float64(count)/float64(len(errorLogs))*100),
								"monitor_config": map[string]any{
									"title":        fmt.Sprintf("Error Pattern: %s", truncate(samples[pattern], 40)),
									"monitor_type": "rule",
									"rule_config": map[string]any{
										"source":    "logs",
										"filter":    map[string]any{"query": truncate(samples[pattern], 60)},
										"metric":    "count",
										"operator":  "gt",
										"threshold": 5,
									},
									"severity":   "warning",
									"schedule":   "*/5 * * * *",
									"time_range": "5m",
								},
							})
						}
					}
				}
			}
		}

		// Standard gap checks.
		if focus == "all" || focus == "health" {
			if !hasMonitorLike(existingMonitors, "connection") && !hasMonitorLike(existingMonitors, "pool") {
				gaps = append(gaps, "No connection pool monitoring")
				suggestions = append(suggestions, map[string]any{
					"priority": "high",
					"category": "health",
					"title":    "Monitor database connection utilization",
					"reason":   "No monitor covering connection pool health or saturation",
					"monitor_config": map[string]any{
						"title":        "Connection Pool Utilization",
						"monitor_type": "rule",
						"rule_config": map[string]any{
							"source":    "query",
							"query":     "SELECT count(*) FROM pg_stat_activity WHERE state != 'idle'",
							"operator":  "gt",
							"threshold": 80,
						},
						"severity":   "warning",
						"schedule":   "*/5 * * * *",
						"time_range": "5m",
					},
				})
			}
			if !hasMonitorLike(existingMonitors, "replication") && !hasMonitorLike(existingMonitors, "replica") {
				gaps = append(gaps, "No replication lag monitoring")
			}
			if !hasMonitorLike(existingMonitors, "vacuum") && !hasMonitorLike(existingMonitors, "dead tuple") {
				gaps = append(gaps, "No dead tuple / vacuum monitoring")
			}
			if !hasMonitorLike(existingMonitors, "disk") {
				gaps = append(gaps, "No disk space monitoring")
			}
		}

		if focus == "all" || focus == "performance" {
			if !hasMonitorLike(existingMonitors, "slow") && !hasMonitorLike(existingMonitors, "query") {
				suggestions = append(suggestions, map[string]any{
					"priority": "medium",
					"category": "performance",
					"title":    "Monitor slow queries",
					"reason":   "No monitor covering query execution time",
					"monitor_config": map[string]any{
						"title":        "Slow Query Alert",
						"monitor_type": "rule",
						"rule_config": map[string]any{
							"source":    "query",
							"query":     "SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND now() - query_start > interval '5 seconds'",
							"operator":  "gt",
							"threshold": 3,
						},
						"severity":   "warning",
						"schedule":   "*/5 * * * *",
						"time_range": "5m",
					},
				})
			}
		}

		if focus == "all" || focus == "security" {
			if !hasMonitorLike(existingMonitors, "auth") && !hasMonitorLike(existingMonitors, "login") {
				gaps = append(gaps, "No authentication failure monitoring")
			}
		}

		// Filter suggestions by focus.
		if focus != "all" {
			var filtered []map[string]any
			for _, s := range suggestions {
				if s["category"] == focus {
					filtered = append(filtered, s)
				}
			}
			suggestions = filtered
		}

		resp := map[string]any{
			"existing_coverage": map[string]any{
				"total_monitors": len(existingMonitors),
				"categories":     coverage,
			},
			"suggestions": suggestions,
		}
		if len(gaps) > 0 {
			resp["gaps"] = gaps
		}

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// categorizeMonitor assigns a category based on monitor title/config.
func categorizeMonitor(m store.Watcher) string {
	title := m.Title
	switch {
	case containsAny(title, "slow", "query", "performance", "index", "latency"):
		return "performance"
	case containsAny(title, "error", "fail", "exception", "log"):
		return "errors"
	case containsAny(title, "connection", "disk", "cpu", "memory", "replication", "vacuum", "health", "pool"):
		return "health"
	case containsAny(title, "auth", "login", "security", "privilege"):
		return "security"
	default:
		return "errors" // default category
	}
}

// hasMonitorLike checks if any existing monitor's title contains the keyword.
func hasMonitorLike(monitors []store.Watcher, keyword string) bool {
	for _, m := range monitors {
		if containsAny(m.Title, keyword) {
			return true
		}
	}
	return false
}

// containsAny checks if s contains any of the given substrings (case-insensitive).
func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
