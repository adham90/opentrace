package events

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the events domain.
var Module = server.Module{
	Name:  "events",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		eventStore:           deps.EventStore,
		codeEntityStore:      deps.CodeEntityStore,
		testCorrelationStore: deps.TestCorrelationStore,
	}

	// Read routes (session auth, applied by caller)
	r.Get("/events", h.list)
	r.Get("/code-entities", h.listCodeEntities)
	r.Get("/test-gaps", h.listTestGaps)

	// Webhook route (API key auth, registered on the API router directly)
	if deps.APIRouter != nil && deps.APIKeyAuth != nil && deps.APIRateLimiter != nil {
		deps.APIRouter.With(deps.APIRateLimiter, deps.APIKeyAuth).Post("/events/{type}", h.webhook)
	}
}
