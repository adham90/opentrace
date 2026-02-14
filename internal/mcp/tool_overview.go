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
		mcp.WithDescription("Get a high-level dashboard of system health: alert counts by severity, watcher status, log error rates, connector and server status. Use proactively at the start of a session to understand the current state of the system."),
	)
}

// systemOverviewHandler returns a handler that builds a system overview.
func systemOverviewHandler(
	alertStore store.AlertStore,
	watcherStore store.WatcherStore,
	logStore store.LogStore,
	dsStore store.DataSourceStore,
	serverStore store.ServerStore,
) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		overview := map[string]any{}

		// Alerts
		if alertStore != nil {
			alerts := map[string]int{"total": 0, "critical": 0, "warning": 0, "info": 0}
			if total, err := alertStore.CountTotal(ctx); err == nil {
				alerts["total"] = total
			}
			oneHourAgo := time.Now().Add(-1 * time.Hour)
			if bySev, err := alertStore.CountBySeverity(ctx, oneHourAgo, time.Now()); err == nil {
				for sev, count := range bySev {
					alerts[sev] = count
				}
			}
			overview["alerts"] = alerts
		}

		// Watchers
		if watcherStore != nil {
			watchers, err := watcherStore.List(ctx, store.ListWatcherParams{})
			if err == nil {
				wStats := map[string]int{
					"total": len(watchers), "active": 0, "paused": 0, "error": 0, "expired": 0,
				}
				for _, w := range watchers {
					switch w.Status {
					case store.WatcherActive:
						wStats["active"]++
					case store.WatcherPaused:
						wStats["paused"]++
					case store.WatcherError:
						wStats["error"]++
					case store.WatcherExpired:
						wStats["expired"]++
					}
				}
				overview["watchers"] = wStats
			}
		}

		// Logs (last hour)
		if logStore != nil {
			now := time.Now()
			counts, err := logStore.CountByLevel(ctx, store.LogCountParams{
				Since: now.Add(-1 * time.Hour),
				Until: now,
			})
			if err == nil {
				total := 0
				errCount := 0
				for level, count := range counts {
					total += count
					if level == "ERROR" {
						errCount = count
					}
				}
				overview["logs"] = map[string]int{
					"last_hour":        total,
					"errors_last_hour": errCount,
				}
			}
		}

		// Connectors
		if dsStore != nil {
			connectors, err := dsStore.List(ctx, store.ListDataSourceParams{})
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
		if serverStore != nil {
			servers, err := serverStore.List(ctx)
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

		data, err := json.MarshalIndent(overview, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal overview: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// triageAlertsTool returns the tool definition for triage_alerts.
func triageAlertsTool() mcp.Tool {
	return mcp.NewTool("triage_alerts",
		mcp.WithDescription("Get a prioritized list of items needing attention: unread alerts, failed watcher runs, error connectors, offline servers. Sorted by severity then recency. Use when the user asks 'what needs attention?' or 'what's broken?'."),
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
func triageAlertsHandler(
	alertStore store.AlertStore,
	runStore store.WatcherRunStore,
	dsStore store.DataSourceStore,
	serverStore store.ServerStore,
) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var items []triageEntry

		// Unread alerts
		if alertStore != nil {
			alerts, err := alertStore.List(ctx, store.ListAlertParams{
				UnreadOnly: true,
				Limit:      10,
			})
			if err == nil {
				for _, a := range alerts {
					items = append(items, triageEntry{
						Type:     "alert",
						Severity: string(a.Severity),
						Title:    a.Title,
						Detail:   a.WatcherTitle,
						Time:     a.CreatedAt.Format(time.RFC3339),
						ID:       a.ID.String(),
					})
				}
			}
		}

		// Recent failed runs
		if runStore != nil {
			runs, err := runStore.ListRecentFailed(ctx, 5)
			if err == nil {
				for _, r := range runs {
					detail := ""
					if r.Error != nil {
						detail = *r.Error
						if len(detail) > 100 {
							detail = detail[:100]
						}
					}
					items = append(items, triageEntry{
						Type:     "failed_run",
						Severity: "warning",
						Title:    "Watcher run failed",
						Detail:   detail,
						Time:     r.CreatedAt.Format(time.RFC3339),
						ID:       r.ID.String(),
					})
				}
			}
		}

		// Error connectors
		if dsStore != nil {
			connectors, err := dsStore.List(ctx, store.ListDataSourceParams{})
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
		if serverStore != nil {
			servers, err := serverStore.List(ctx)
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

		data, err := json.MarshalIndent(items, "", "  ")
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
