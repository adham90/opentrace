package errorimpact

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the error impact domain.
var Module = server.Module{
	Name:  "errorimpact",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — error impact data is accessed via MCP tools.
}
