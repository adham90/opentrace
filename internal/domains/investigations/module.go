package investigations

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the investigation sessions domain.
var Module = server.Module{
	Name:  "investigations",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — investigations are accessed via MCP tools.
}
