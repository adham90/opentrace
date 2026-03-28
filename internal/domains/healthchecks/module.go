package healthchecks

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the health checks domain.
var Module = server.Module{
	Name:  "healthchecks",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — health checks are managed via MCP tools.
}
