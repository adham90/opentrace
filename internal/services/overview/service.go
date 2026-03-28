// Package overview provides business logic for the system overview domain.
// It aggregates data from multiple stores into typed reports, independent
// of any transport (MCP, HTTP, CLI).
package overview

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Service aggregates data from multiple stores for system overview reports.
type Service struct {
	Logs         store.LogStore
	ErrorGroups  store.ErrorGroupStore
	Watches      store.WatchStore
	HealthChecks store.HealthCheckStore
	DataSources  store.DataSourceStore
	Servers      store.ServerStore
}

// StatusReport is the result of a system status check.
type StatusReport struct {
	Logs         *LogStats        `json:"logs,omitempty"`
	ErrorGroups  *ErrorGroupStats `json:"error_groups,omitempty"`
	WatchAlerts  *AlertStats      `json:"watch_alerts,omitempty"`
	HealthChecks *HealthStats     `json:"healthchecks,omitempty"`
	Connectors   *ConnectorStats  `json:"connectors,omitempty"`
	Servers      *ServerStats     `json:"servers,omitempty"`
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

// Status returns a snapshot of system health across all subsystems.
func (s *Service) Status(ctx context.Context) (*StatusReport, error) {
	report := &StatusReport{}

	if s.Logs != nil {
		now := time.Now()
		counts, err := s.Logs.CountByLevel(ctx, store.LogCountParams{
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
			report.Logs = &LogStats{
				LastHour:       total,
				ErrorsLastHour: errCount,
			}
		}
	}

	if s.ErrorGroups != nil {
		unresolvedCount, err := s.ErrorGroups.Count(ctx, store.ErrorGroupUnresolved)
		if err == nil && unresolvedCount > 0 {
			report.ErrorGroups = &ErrorGroupStats{Unresolved: unresolvedCount}
		}
	}

	if s.Watches != nil {
		pendingCount, err := s.Watches.CountPendingAlerts(ctx)
		if err == nil && pendingCount > 0 {
			report.WatchAlerts = &AlertStats{Pending: pendingCount}
		}
	}

	if s.HealthChecks != nil {
		summaries, err := s.HealthChecks.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
		if err == nil && len(summaries) > 0 {
			hs := &HealthStats{Total: len(summaries)}
			for _, sum := range summaries {
				switch store.HealthCheckStatus(sum.CurrentStatus) {
				case store.HealthCheckDown:
					hs.Down++
				case store.HealthCheckDegraded:
					hs.Degraded++
				}
			}
			report.HealthChecks = hs
		}
	}

	if s.DataSources != nil {
		connectors, err := s.DataSources.List(ctx, store.ListDataSourceParams{})
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

	if s.Servers != nil {
		servers, err := s.Servers.List(ctx, store.ListServerParams{})
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

	return report, nil
}

// HasErrors returns true if there are unresolved error groups.
func (r *StatusReport) HasErrors() bool {
	return r.ErrorGroups != nil && r.ErrorGroups.Unresolved > 0
}

// HasLogErrors returns true if there were errors in the last hour.
func (r *StatusReport) HasLogErrors() bool {
	return r.Logs != nil && r.Logs.ErrorsLastHour > 0
}

// HasDownChecks returns true if health checks are failing.
func (r *StatusReport) HasDownChecks() bool {
	return r.HealthChecks != nil && r.HealthChecks.Down > 0
}
