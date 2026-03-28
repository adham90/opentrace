// Package overview provides business logic for the system overview domain.
// It aggregates data from multiple stores into typed reports, independent
// of any transport (MCP, HTTP, CLI).
//
// The repository interfaces define only the methods the overview service
// actually needs, keeping dependencies minimal.
package overview

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// LogCounter counts log entries by level within a time window.
type LogCounter interface {
	CountByLevel(ctx context.Context, params store.LogCountParams) (map[string]int, error)
}

// ErrorCounter counts error groups by status.
type ErrorCounter interface {
	Count(ctx context.Context, status store.ErrorGroupStatus) (int, error)
}

// AlertCounter counts pending watch alerts.
type AlertCounter interface {
	CountPendingAlerts(ctx context.Context) (int, error)
}

// HealthSummariser returns uptime summaries for health checks.
type HealthSummariser interface {
	UptimeSummaries(ctx context.Context, since time.Time) ([]store.UptimeSummary, error)
}

// ConnectorLister lists data source connectors.
type ConnectorLister interface {
	List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error)
}

// ServerLister lists monitored servers.
type ServerLister interface {
	List(ctx context.Context, params store.ListServerParams) ([]store.Server, error)
}
