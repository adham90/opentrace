package logs

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the logs domain.
var Module = server.Module{
	Name:  "logs",
	Mount: mount,
}

func mount(_ chi.Router, _ *server.Deps) {
	// All REST read routes removed — logs are queried via MCP tools.
	// Log ingestion is handled by POST /api/logs in internal/api/server.go.
}
