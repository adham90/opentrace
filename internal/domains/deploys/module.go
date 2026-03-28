package deploys

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the deploys domain.
var Module = server.Module{
	Name:  "deploys",
	Mount: mount,
}

func mount(_ chi.Router, deps *server.Deps) {
	h := &handler{
		store: deps.DeployStore,
	}

	// Webhook route (API key auth, registered on the API router directly)
	if deps.APIRouter != nil && deps.APIKeyAuth != nil && deps.APIRateLimiter != nil {
		deps.APIRouter.With(deps.APIRateLimiter, deps.APIKeyAuth).Post("/events/deploy", h.webhook)
	}
}
