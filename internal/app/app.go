// Package app wires together domain services, adapters, and configuration.
// It is the single construction point for the application — main.go creates
// an App, then passes its services to transport layers (MCP, HTTP, CLI).
package app

import (
	"github.com/adham90/opentrace/internal/domain/admin"
	"github.com/adham90/opentrace/internal/domain/analytics"
	"github.com/adham90/opentrace/internal/domain/auth"
	"github.com/adham90/opentrace/internal/domain/code"
	domainconnectors "github.com/adham90/opentrace/internal/domain/connectors"
	"github.com/adham90/opentrace/internal/domain/database"
	"github.com/adham90/opentrace/internal/domain/deploys"
	domainerrors "github.com/adham90/opentrace/internal/domain/errors"
	"github.com/adham90/opentrace/internal/domain/healthchecks"
	"github.com/adham90/opentrace/internal/domain/logs"
	"github.com/adham90/opentrace/internal/domain/overview"
	"github.com/adham90/opentrace/internal/domain/servers"
	"github.com/adham90/opentrace/internal/domain/setup"
	"github.com/adham90/opentrace/internal/domain/watches"
	"github.com/adham90/opentrace/internal/telemetry"
	"github.com/adham90/opentrace/pkg/store"
)

// App holds all domain services, constructed from store adapters.
// Pass this to transport layers (MCP, HTTP) so they can call domain logic.
type App struct {
	// Domain services
	Logs         *logs.Service
	Errors       *domainerrors.Service
	Overview     *overview.Service
	Analytics    *analytics.Service
	Watches      *watches.Service
	Deploys      *deploys.Service
	HealthChecks *healthchecks.Service
	Servers      *servers.Service
	Database     *database.Service
	Code         *code.Service
	Admin        *admin.Service
	Connectors   *domainconnectors.Service
	Setup        *setup.Service
	Auth         *auth.Service

	// Telemetry
	Startup    *telemetry.Startup
	StoreStats *telemetry.StoreMetrics
	Workers    *telemetry.GoroutineHealth
}

// New constructs all domain services from the provided stores.
// This is the single wiring point — add new services here.
func New(stores store.Stores) *App {
	startup := telemetry.NewStartup()
	storeStats := telemetry.NewStoreMetrics()
	workers := telemetry.NewGoroutineHealth()

	app := &App{
		Startup:    startup,
		StoreStats: storeStats,
		Workers:    workers,
	}

	// Logs service
	if stores.LogStore != nil {
		app.Logs = logs.NewService(stores.LogStore)
	}

	// Errors service
	if stores.ErrorGroupStore != nil {
		app.Errors = domainerrors.NewService(stores.ErrorGroupStore, stores.ErrorImpactStore)
	}

	// Overview service (uses minimal interfaces — stores implement them)
	app.Overview = overview.NewService(
		overview.WithLogCounter(stores.LogStore),
		overview.WithErrorCounter(stores.ErrorGroupStore),
		overview.WithAlertCounter(stores.WatchStore),
		overview.WithHealthSummariser(stores.HealthCheckStore),
		overview.WithConnectorLister(stores.DSStore),
		overview.WithServerLister(stores.ServerStore),
	)

	// Analytics service
	if stores.AnalyticsStore != nil {
		app.Analytics = analytics.NewService(stores.AnalyticsStore, stores.TrendStore)
	}

	// Watches service
	if stores.WatchStore != nil {
		app.Watches = watches.NewService(stores.WatchStore)
	}

	// Deploys service
	if stores.DeployStore != nil {
		app.Deploys = deploys.NewService(stores.DeployStore)
	}

	// HealthChecks service
	if stores.HealthCheckStore != nil {
		app.HealthChecks = healthchecks.NewService(stores.HealthCheckStore)
	}

	// Servers service
	if stores.ServerStore != nil {
		app.Servers = servers.NewService(stores.ServerStore, stores.MetricStore)
	}

	// Code service
	if stores.CodeEntityStore != nil || stores.TestCorrelationStore != nil {
		app.Code = code.NewService(stores.CodeEntityStore, stores.TestCorrelationStore, stores.AgentNoteStore)
	}

	// Admin service
	app.Admin = admin.NewService(stores.SettingsStore, stores.AuditStore)

	// Connectors service
	if stores.DSStore != nil {
		app.Connectors = domainconnectors.NewService(stores.DSStore)
	}

	// Setup service
	app.Setup = setup.NewService(stores.LogStore, stores.UserStore, stores.DSStore)

	// Auth service
	if stores.UserStore != nil {
		app.Auth = auth.NewService(stores.UserStore)
	}

	return app
}
