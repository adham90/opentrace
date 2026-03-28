package mcpactivity

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the MCP activity domain.
var Module = server.Module{
	Name:  "mcpactivity",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — MCP activity is accessed via MCP tools.
}
