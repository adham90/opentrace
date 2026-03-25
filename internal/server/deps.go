// Package server provides shared HTTP infrastructure for all domain modules.
// Deps holds injected dependencies. Module describes a domain's contributions.
// Domains import this package; this package never imports domains.
package server

import (
	"database/sql"

	"github.com/go-chi/chi/v5"

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
	InvestigationSessionStore    store.InvestigationSessionStore
	ToolTransitionStore          store.ToolTransitionStore
	WorkflowTemplateStore        store.WorkflowTemplateStore
	QueryMemoryStore             store.QueryMemoryStore
	RunbookEffectivenessStore    store.RunbookEffectivenessStore
	CodeEntityStore              store.CodeEntityStore
	DeployStore               store.DeployStore
	EventStore                store.EventStore
	TestCorrelationStore      store.TestCorrelationStore

	// Infrastructure
	Registry     *connector.Registry
	Queue        *jobs.Queue
	WatchMetrics *watcher.WatchMetrics

	// PageRouter is the top-level router group for page routes (with auth
	// and onboarding-redirect middleware already applied). Modules that
	// need to register HTML page handlers use this instead of the API
	// sub-router passed to Mount.
	PageRouter chi.Router

	// AdminPageRouter is the top-level router group for admin-only page
	// routes (with auth, onboarding-redirect, and RequireAdmin middleware
	// already applied). Modules that need admin HTML pages use this.
	AdminPageRouter chi.Router
}
