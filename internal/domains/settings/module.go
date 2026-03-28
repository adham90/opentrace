package settings

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the settings domain.
var Module = server.Module{
	Name:  "settings",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — settings are managed via MCP tools.
}
