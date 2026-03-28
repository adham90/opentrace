package events

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the events domain.
var Module = server.Module{
	Name:  "events",
	Mount: mount,
}

func mount(_ chi.Router, deps *server.Deps) {
	h := &handler{
		eventStore: deps.EventStore,
	}

	// Webhook route (API key auth, registered on the API router directly)
	if deps.APIRouter != nil && deps.APIKeyAuth != nil && deps.APIRateLimiter != nil {
		deps.APIRouter.With(deps.APIRateLimiter, deps.APIKeyAuth).Post("/events/{type}", h.webhook)
	}
}
