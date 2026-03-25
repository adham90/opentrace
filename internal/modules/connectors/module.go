package connectors

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the connectors domain.
var Module = server.Module{
	Name:  "connectors",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		dsStore:       deps.DSStore,
		logStore:      deps.LogStore,
		settingsStore: deps.SettingsStore,
		registry:      deps.Registry,
		cfg:           deps.Cfg,
	}

	// Read API
	r.Get("/connectors", h.listConnectors)
	r.Get("/connectors/{id}", h.getConnector)

	// Write API (admin only)
	r.Group(func(r chi.Router) {
		r.Use(server.RequireAdminAPI)
		r.Post("/connectors", h.createConnector)
		r.Put("/connectors/{id}", h.updateConnector)
		r.Post("/connectors/{id}/test", h.testConnector)
		r.Delete("/connectors/{id}", h.deleteConnector)
	})

	// Page routes (mounted on the page router with auth middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/connectors", h.connectorsPage)
	}
}
