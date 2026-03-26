package servers

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
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

	// Read/write routes (session auth, applied by caller)
	r.Get("/servers", h.list)
	r.Get("/servers/{id}", h.get)
	r.Put("/servers/{id}", h.update)
	r.Delete("/servers/{id}", h.delete)
	r.Get("/servers/{id}/metrics", h.queryMetrics)

	// Webhook and agent routes (API key auth or no auth, registered on the
	// API router directly so they bypass the session-auth group).
	if deps.APIRouter != nil && deps.APIKeyAuth != nil && deps.APIRateLimiter != nil {
		deps.APIRouter.With(deps.APIRateLimiter, deps.APIKeyAuth).Post("/servers/register", h.register)
		deps.APIRouter.With(deps.APIRateLimiter, deps.APIKeyAuth).Post("/servers/{id}/metrics", h.pushMetrics)
		// Agent install script (no auth -- the script is self-contained)
		deps.APIRouter.Get("/agent/install.sh", h.agentInstallScript)
	}
}
