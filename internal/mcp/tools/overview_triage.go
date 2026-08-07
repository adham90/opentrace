package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// --- triage action ---

type triageEntry struct {
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	Time        string `json:"time"`
	ID          string `json:"id"`
	Environment string `json:"environment,omitempty"`
}

func HandleTriage(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
	// Triage is a listing like any other, so it takes the caller's env scope.
	// It used to query unfiltered, which made it the one tool that would show a
	// production-scoped caller staging's error groups — and since every
	// drill-down tool does enforce scope, the fingerprints it handed back could
	// not be opened by the caller it handed them to.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	var items []triageEntry

	// Unresolved error groups (highest priority)
	if d.ErrorGroupStore != nil {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Status:      store.ErrorGroupUnresolved,
			Environment: env,
			SortBy:      "occurrence_count",
			Limit:       10,
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
				detail := fmt.Sprintf("%d occurrences, last seen %s", eg.OccurrenceCount, eg.LastSeenAt.Format(time.RFC3339))
				// Name the env when the caller can see more than one, so two
				// rows for the same fingerprint are distinguishable.
				if env == "" && eg.Environment != "" {
					detail += " [" + eg.Environment + "]"
				}
				items = append(items, triageEntry{
					Type:        "error_group",
					Severity:    "critical",
					Title:       title,
					Detail:      detail,
					Time:        eg.LastSeenAt.Format(time.RFC3339),
					ID:          eg.Fingerprint,
					Environment: eg.Environment,
				})
			}
		}
	}

	// Active watch alerts
	if d.WatchStore != nil {
		alerts, err := d.WatchStore.ListAlerts(ctx, "", "pending", 10)
		if err == nil {
			for _, a := range alerts {
				// ListAlerts has no env filter, so drop out-of-scope alerts here.
				if env != "" && a.Environment != env {
					continue
				}
				items = append(items, triageEntry{
					Type:        "watch_alert",
					Severity:    "warning",
					Title:       a.Summary,
					Detail:      fmt.Sprintf("%s: %.2f (threshold: %.2f)", a.TriggerMetric(), a.TriggerValue(), a.ThresholdValue()),
					Time:        a.CreatedAt.Format(time.RFC3339),
					ID:          a.ID,
					Environment: a.Environment,
				})
			}
		}
	}

	// Down healthchecks
	if d.HealthCheckStore != nil {
		summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
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
	if d.DSStore != nil {
		connectors, err := d.DSStore.List(ctx, store.ListDataSourceParams{})
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
	if d.ServerStore != nil {
		servers, err := d.ServerStore.List(ctx, store.ListServerParams{})
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
		return EmptyResult("Nothing needs attention. System looks healthy.")
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
		// Carry the env into the suggestion. A fingerprint alone is ambiguous
		// when it exists in several envs, and following the suggestion would
		// land on whichever row was touched most recently.
		detailArgs := map[string]any{
			"action":      "detail",
			"fingerprint": top.ID,
		}
		if top.Environment != "" {
			detailArgs["environment"] = top.Environment
		}
		suggestions = append(suggestions, Suggest("errors", "Investigate top error", detailArgs))
	case "watch_alert":
		suggestions = append(suggestions, Suggest("watches", "Investigate top alert", map[string]any{
			"action":   "investigate",
			"alert_id": top.ID,
		}))
	case "healthcheck":
		suggestions = append(suggestions, Suggest("healthchecks", "Check endpoint uptime details", map[string]any{
			"action": "uptime",
		}))
	}
	if len(items) > 1 {
		suggestions = append(suggestions, Suggest("overview", "Full system investigation", map[string]any{"action": "diagnose"}))
	}
	return JSONResult(resp, suggestions...)
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
