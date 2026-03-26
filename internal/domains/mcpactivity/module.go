package mcpactivity

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the MCP activity domain.
var Module = server.Module{
	Name:  "mcpactivity",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		store: deps.MCPActivityStore,
	}

	r.Get("/mcp/activity/stats", h.stats)
	r.Get("/mcp/activity", h.list)
}
