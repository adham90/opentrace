package watches

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the watches domain.
var Module = server.Module{
	Name:  "watches",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		watchStore:                deps.WatchStore,
		logStore:                  deps.LogStore,
		watchMetrics:              deps.WatchMetrics,
		auditStore:                deps.AuditStore,
		investigationSessionStore: deps.InvestigationSessionStore,
		cfg:                       deps.Cfg,
	}

	if deps.WatchStore == nil {
		return
	}

	// Read API
	r.Get("/watches", h.list)
	r.Get("/watches/{id}", h.get)
	r.Get("/watches/{id}/runs", h.listRuns)
	r.Get("/watches/{id}/alerts", h.listAlerts)
	r.Get("/watches/alerts/{alertId}", h.getAlert)
	r.Get("/watches/alerts/count", h.alertCount)

	// Write API (admin only)
	r.Group(func(r chi.Router) {
		r.Use(server.RequireAdminAPI)
		r.Post("/watches", h.create)
		r.Delete("/watches/{id}", h.delete)
		r.Post("/watches/alerts/{alertId}/dismiss", h.dismissAlert)
		r.Post("/watches/alerts/{alertId}/acknowledge", h.acknowledgeAlert)
	})

	// Page routes (mounted on the page router with auth middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/watchers", h.watchersPage)
		deps.PageRouter.Get("/watchers/{id}", h.watchDetailPage)
	}
}
