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

func mount(_ chi.Router, deps *server.Deps) {
	h := &handler{
		serverStore: deps.ServerStore,
		metricStore: deps.MetricStore,
		dsStore:     deps.DSStore,
		registry:    deps.Registry,
	}

	// SDK ingestion routes (API key auth, registered on the API router
	// directly so they bypass the session-auth group).
	if deps.APIRouter != nil && deps.APIKeyAuth != nil && deps.APIRateLimiter != nil {
		deps.APIRouter.With(deps.APIRateLimiter, deps.APIKeyAuth).Post("/servers/register", h.register)
		deps.APIRouter.With(deps.APIRateLimiter, deps.APIKeyAuth).Post("/servers/{id}/metrics", h.pushMetrics)
	}
}
