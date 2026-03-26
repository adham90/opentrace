package dashboard

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the dashboard domain.
var Module = server.Module{
	Name:  "dashboard",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		logStore:                  deps.LogStore,
		watchStore:                deps.WatchStore,
		healthCheckStore:          deps.HealthCheckStore,
		errorGroupStore:           deps.ErrorGroupStore,
		serverStore:               deps.ServerStore,
		metricStore:               deps.MetricStore,
		investigationSessionStore: deps.InvestigationSessionStore,
		mcpActivityStore:          deps.MCPActivityStore,
		analyticsStore:            deps.AnalyticsStore,
		deployStore:               deps.DeployStore,
		dsStore:                   deps.DSStore,
		db:                        deps.DB,
		cfg:                       deps.Cfg,
	}

	// API routes (mounted under /api with auth middleware by the server)
	r.Get("/dashboard", h.api)
	r.Get("/overview", h.overview)

	// Page route (mounted on the root router with page middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/", h.page)
	}
}
