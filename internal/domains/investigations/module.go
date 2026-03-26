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
		sessionStore:     deps.InvestigationSessionStore,
		mcpActivityStore: deps.MCPActivityStore,
		cfg:              deps.Cfg,
	}

	r.Get("/investigations", h.list)
	r.Get("/investigations/stats", h.stats)
	r.Get("/investigations/{id}", h.get)
	r.Get("/investigations/{id}/steps", h.steps)

	// Page routes (mounted on the page router with auth middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/sessions", h.sessionsPage)
		deps.PageRouter.Get("/sessions/{id}", h.sessionDetailPage)
	}
}
