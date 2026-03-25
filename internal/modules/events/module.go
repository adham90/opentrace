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

	r.Post("/events/{type}", h.webhook)
	r.Get("/events", h.list)
	r.Get("/code-entities", h.listCodeEntities)
	r.Get("/test-gaps", h.listTestGaps)
}
