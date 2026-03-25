package servers

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the servers and metrics domain.
var Module = server.Module{
	Name:  "servers",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		serverStore: deps.ServerStore,
		metricStore: deps.MetricStore,
		dsStore:     deps.DSStore,
		registry:    deps.Registry,
	}

	r.Post("/servers/register", h.register)
	r.Get("/servers", h.list)
	r.Get("/servers/{id}", h.get)
	r.Put("/servers/{id}", h.update)
	r.Delete("/servers/{id}", h.delete)
	r.Post("/servers/{id}/metrics", h.pushMetrics)
	r.Get("/servers/{id}/metrics", h.queryMetrics)
	r.Get("/agent/install.sh", h.agentInstallScript)
}
