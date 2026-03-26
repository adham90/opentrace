package healthchecks

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the health checks domain.
var Module = server.Module{
	Name:  "healthchecks",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		store: deps.HealthCheckStore,
		cfg:   deps.Cfg,
	}

	r.Get("/healthchecks", h.list)
	r.Post("/healthchecks", h.create)
	r.Get("/healthchecks/{id}/results", h.results)
	r.Delete("/healthchecks/{id}", h.delete)
	r.Get("/uptime/summary", h.uptimeSummary)

	// Page routes (mounted on the page router with auth middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/health", h.healthPage)
	}
}
