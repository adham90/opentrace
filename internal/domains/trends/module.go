package trends

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the trends and analytics domain.
var Module = server.Module{
	Name:  "trends",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — trends/analytics are accessed via MCP tools.
}
