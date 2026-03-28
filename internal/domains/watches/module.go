package watches

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the watches domain.
var Module = server.Module{
	Name:  "watches",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — watches are managed via MCP tools.
}
