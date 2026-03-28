package connectors

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the connectors domain.
var Module = server.Module{
	Name:  "connectors",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — connectors are managed via MCP tools
	// which call the store layer directly.
}
