package errors

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the error groups domain.
var Module = server.Module{
	Name:  "errors",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST routes removed — error groups are accessed via MCP tools.
}
