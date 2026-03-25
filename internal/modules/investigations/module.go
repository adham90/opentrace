package investigations

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the investigation sessions domain.
var Module = server.Module{
	Name:  "investigations",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		sessionStore:    deps.InvestigationSessionStore,
		mcpActivityStore: deps.MCPActivityStore,
	}

	r.Get("/investigations", h.list)
	r.Get("/investigations/stats", h.stats)
	r.Get("/investigations/{id}", h.get)
	r.Get("/investigations/{id}/steps", h.steps)
}
