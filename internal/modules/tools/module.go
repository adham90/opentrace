package tools

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the tools domain.
var Module = server.Module{
	Name:  "tools",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		toolCatalog: deps.ToolCatalog,
		registry:    deps.Registry,
		cfg:         deps.Cfg,
	}

	// Read API
	r.Get("/tools", h.toolsAPI)

	// Page routes (mounted on the page router with auth middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/tools", h.toolsPage)
	}
}
