// Package server provides shared HTTP infrastructure for all domain modules.
// Deps holds injected dependencies. Module describes a domain's contributions.
// Domains import this package; this package never imports domains.
package server

import (
	"database/sql"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/jobs"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// Deps holds shared dependencies injected into every domain module.
// Adding a new dependency = one field here, one line in cmd/opentrace/main.go.
type Deps struct {
	DB  *sql.DB
	Cfg *config.Config

	// Stores
	DSStore                   store.DataSourceStore
	LogStore                  store.LogStore
	ServerStore               store.ServerStore
	MetricStore               store.MetricStore
	UserStore                 store.UserStore
	SessionStore              store.SessionStore
	SettingsStore             store.SettingsStore
	MCPActivityStore          store.MCPActivityStore
	AuditStore                store.AuditStore
	WatchStore                store.WatchStore
	ErrorGroupStore           store.ErrorGroupStore
	HealthCheckStore          store.HealthCheckStore
	AgentNoteStore            store.AgentNoteStore
	TrendStore                store.TrendStore
	AnalyticsStore            store.AnalyticsStore
	JourneyStore              store.JourneyStore
	ErrorImpactStore          store.ErrorImpactStore
	TraceStore                store.TraceStore
	InvestigationSessionStore store.InvestigationSessionStore
	CodeEntityStore           store.CodeEntityStore
	DeployStore               store.DeployStore
	EventStore                store.EventStore
	TestCorrelationStore      store.TestCorrelationStore

	// Infrastructure
	Registry     *connector.Registry
	Queue        *jobs.Queue
	WatchMetrics *watcher.WatchMetrics
}
