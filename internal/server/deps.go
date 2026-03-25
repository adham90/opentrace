// Package server provides shared HTTP infrastructure for all domain modules.
// Deps holds injected dependencies. Module describes a domain's contributions.
// Domains import this package; this package never imports domains.
package server

import (
	"database/sql"
	"net/http"

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

	// ToolCatalog provides the MCP tool catalog for the tools page/API.
	// Typed as interface to avoid importing internal/mcp.
	ToolCatalog ToolCatalogProvider

	// PageRouter is the top-level router group for page routes (with auth
	// and onboarding-redirect middleware already applied). Modules that
	// need to register HTML page handlers use this instead of the API
	// sub-router passed to Mount.
	PageRouter chi.Router

	// AdminPageRouter is the top-level router group for admin-only page
	// routes (with auth, onboarding-redirect, and RequireAdmin middleware
	// already applied). Modules that need admin HTML pages use this.
	AdminPageRouter chi.Router

	// APIRouter is the top-level /api router (with decompression and CORS
	// but no session auth). Modules use this to register routes that need
	// API key auth instead of session auth (webhooks, agent endpoints).
	APIRouter chi.Router

	// APIKeyAuth is middleware that validates requests using the configured
	// API key. Modules apply this on webhook/ingestion routes that accept
	// machine-to-machine traffic instead of browser sessions.
	APIKeyAuth func(http.Handler) http.Handler

	// APIRateLimiter is rate-limiting middleware for API endpoints.
	APIRateLimiter func(http.Handler) http.Handler

	// RootRouter is the top-level chi.Router (before any auth middleware
	// groups). Modules that need to register unauthenticated routes
	// (e.g. login, register, onboarding) use this.
	RootRouter chi.Router

	// LoginLimiter is rate-limiting middleware for login/register endpoints.
	LoginLimiter func(http.Handler) http.Handler

	// SecureCookies indicates whether cookies should have the Secure flag.
	SecureCookies bool
}
