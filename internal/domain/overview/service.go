package overview

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Service aggregates data from multiple stores for system overview reports.
type Service struct {
	Logs         LogCounter
	ErrorGroups  ErrorCounter
	Watches      AlertCounter
	HealthChecks HealthSummariser
	DataSources  ConnectorLister
	Servers      ServerLister
}

// NewService creates an overview service with the given dependencies.
// Any dependency may be nil; the Status method gracefully skips nil stores.
func NewService(opts ...Option) *Service {
	s := &Service{}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Option configures a Service dependency.
type Option func(*Service)

// WithLogCounter sets the log counter.
func WithLogCounter(lc LogCounter) Option {
	return func(s *Service) { s.Logs = lc }
}

// WithErrorCounter sets the error group counter.
func WithErrorCounter(ec ErrorCounter) Option {
	return func(s *Service) { s.ErrorGroups = ec }
}

// WithAlertCounter sets the watch alert counter.
func WithAlertCounter(ac AlertCounter) Option {
	return func(s *Service) { s.Watches = ac }
}

// WithHealthSummariser sets the health check summariser.
func WithHealthSummariser(hs HealthSummariser) Option {
	return func(s *Service) { s.HealthChecks = hs }
}

// WithConnectorLister sets the connector lister.
func WithConnectorLister(cl ConnectorLister) Option {
	return func(s *Service) { s.DataSources = cl }
}

// WithServerLister sets the server lister.
func WithServerLister(sl ServerLister) Option {
	return func(s *Service) { s.Servers = sl }
}

// ---------------------------------------------------------------------------
// Report types
// ---------------------------------------------------------------------------

// StatusReport is the result of a system status check.
type StatusReport struct {
	Logs         *LogStats        `json:"logs,omitempty"`
	ErrorGroups  *ErrorGroupStats `json:"error_groups,omitempty"`
	WatchAlerts  *AlertStats      `json:"watch_alerts,omitempty"`
	HealthChecks *HealthStats     `json:"healthchecks,omitempty"`
	Connectors   *ConnectorStats  `json:"connectors,omitempty"`
	Servers      *ServerStats     `json:"servers,omitempty"`
}

// LogStats summarises log volume in a time window.
type LogStats struct {
	LastHour       int `json:"last_hour"`
	ErrorsLastHour int `json:"errors_last_hour"`
}

// ErrorGroupStats summarises unresolved error groups.
type ErrorGroupStats struct {
	Unresolved int `json:"unresolved"`
}

// AlertStats summarises pending watch alerts.
type AlertStats struct {
	Pending int `json:"pending"`
}

// HealthStats summarises health check statuses.
type HealthStats struct {
	Total    int `json:"total"`
	Down     int `json:"down"`
	Degraded int `json:"degraded,omitempty"`
}

// ConnectorStats summarises data source connector statuses.
type ConnectorStats struct {
	Total     int `json:"total"`
	Connected int `json:"connected"`
	Error     int `json:"error"`
}

// ServerStats summarises monitored server statuses.
type ServerStats struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
}

// ---------------------------------------------------------------------------
// StatusReport helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status returns a snapshot of system health across all subsystems.
// Nil stores are gracefully skipped. Store errors are swallowed so that
// a single failing subsystem does not block the entire report.
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
