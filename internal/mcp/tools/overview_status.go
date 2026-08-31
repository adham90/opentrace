package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Typed response structs for overview status
// ---------------------------------------------------------------------------

// StatusReport is the typed response for the overview status action.
// onCallStaleAfter is how long without a successful run before the agent is
// reported as stale. Generous: a quiet week with no alerts is normal, and
// crying wolf about a healthy agent is how the field gets ignored.
const onCallStaleAfter = 14 * 24 * time.Hour

type StatusReport struct {
	Logs         *LogStats        `json:"logs,omitempty"`
	ErrorGroups  *ErrorGroupStats `json:"error_groups,omitempty"`
	WatchAlerts  *AlertStats      `json:"watch_alerts,omitempty"`
	HealthChecks *HealthStats     `json:"healthchecks,omitempty"`
	Connectors   *ConnectorStats  `json:"connectors,omitempty"`
	Servers      *ServerStats     `json:"servers,omitempty"`
	OnCall       *OnCallStats     `json:"oncall,omitempty"`
}

// OnCallStats reports whether the on-call agent is still working.
//
// The subscription token behind the agent CLI expires eventually, and when it
// does triage stops with no error anyone sees — the same silence the dead man's
// switch exists to break, one layer up. Surfacing the last success is how that
// gets noticed before the next incident rather than during it.
type OnCallStats struct {
	Enabled     bool   `json:"enabled"`
	LastSuccess string `json:"last_success,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	RunsToday   int    `json:"runs_today"`
	Stale       bool   `json:"stale,omitempty"`
}

type LogStats struct {
	LastHour       int `json:"last_hour"`
	ErrorsLastHour int `json:"errors_last_hour"`
}

type ErrorGroupStats struct {
	Unresolved int `json:"unresolved"`
}

type AlertStats struct {
	Pending int `json:"pending"`
}

type HealthStats struct {
	Total    int `json:"total"`
	Down     int `json:"down"`
	Degraded int `json:"degraded,omitempty"`
}

type ConnectorStats struct {
	Total     int `json:"total"`
	Connected int `json:"connected"`
	Error     int `json:"error"`
}

type ServerStats struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

func HandleOverviewStatus(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
	// Status is a health summary, so its numbers must describe the env the
	// caller can actually inspect — otherwise it reports a problem count that
	// no drill-down the caller is allowed to run can account for.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	report := &StatusReport{}

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
				if level == "error" || level == "fatal" {
					errCount += count
				}
			}
			report.Logs = &LogStats{
				LastHour:       total,
				ErrorsLastHour: errCount,
			}
		}
	}

	// Unresolved error groups
	if d.ErrorGroupStore != nil {
		unresolvedCount, err := d.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved, env)
		if err == nil && unresolvedCount > 0 {
			report.ErrorGroups = &ErrorGroupStats{Unresolved: unresolvedCount}
		}
	}

	// Active watch alerts
	if d.WatchStore != nil {
		pendingCount, err := d.WatchStore.CountPendingAlerts(ctx)
		if err == nil && pendingCount > 0 {
			report.WatchAlerts = &AlertStats{Pending: pendingCount}
		}
	}

	// Health checks
	if d.HealthCheckStore != nil {
		summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
		if err == nil && len(summaries) > 0 {
			hs := &HealthStats{Total: len(summaries)}
			for _, s := range summaries {
				switch store.HealthCheckStatus(s.CurrentStatus) {
				case store.HealthCheckDown:
					hs.Down++
				case store.HealthCheckDegraded:
					hs.Degraded++
				}
			}
			report.HealthChecks = hs
		}
	}

	// Connectors
	if d.DSStore != nil {
		connectors, err := d.DSStore.List(ctx, store.ListDataSourceParams{})
		if err == nil {
			cs := &ConnectorStats{Total: len(connectors)}
			for _, c := range connectors {
				switch c.Status {
				case store.StatusConnected:
					cs.Connected++
				case store.StatusError:
					cs.Error++
				}
			}
			report.Connectors = cs
		}
	}

	// Servers
	if d.ServerStore != nil {
		servers, err := d.ServerStore.List(ctx, store.ListServerParams{})
		if err == nil {
			ss := &ServerStats{Total: len(servers)}
			for _, srv := range servers {
				switch srv.Status {
				case store.ServerOnline:
					ss.Online++
				case store.ServerOffline:
					ss.Offline++
				}
			}
			report.Servers = ss
		}
	}

	// On-call agent health
	if d.OnCallStatus != nil {
		lastOK, lastErr, runs := d.OnCallStatus()
		oc := &OnCallStats{Enabled: true, LastError: lastErr, RunsToday: runs}
		if !lastOK.IsZero() {
			oc.LastSuccess = lastOK.Format(time.RFC3339)
			oc.Stale = time.Since(lastOK) > onCallStaleAfter
		}
		report.OnCall = oc
	}

	// Suggested next tools based on findings.
	var suggestions []ToolSuggestion
	if report.ErrorGroups != nil && report.ErrorGroups.Unresolved > 0 {
		suggestions = append(suggestions, Suggest("errors", fmt.Sprintf("%d unresolved errors", report.ErrorGroups.Unresolved), map[string]any{"action": "list", "status": "unresolved"}))
	}
	if report.Logs != nil && report.Logs.ErrorsLastHour > 0 {
		suggestions = append(suggestions, Suggest("logs", "Investigate errors in last hour", map[string]any{"action": "summary"}))
	}
	if report.OnCall != nil && (report.OnCall.Stale || report.OnCall.LastError != "") {
		suggestions = append(suggestions, Suggest("overview", "On-call agent may be broken — check its credentials", map[string]any{"action": "settings"}))
	}
	if report.HealthChecks != nil && report.HealthChecks.Down > 0 {
		suggestions = append(suggestions, Suggest("healthchecks", fmt.Sprintf("%d endpoints down", report.HealthChecks.Down), map[string]any{"action": "uptime"}))
	}
	suggestions = append(suggestions, Suggest("overview", "Deep dive into a specific service", map[string]any{"action": "diagnose"}))

	return JSONResult(report, suggestions...)
}
