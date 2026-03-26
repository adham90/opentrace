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

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		logStore: deps.LogStore,
		db:       deps.DB,
		cfg:      deps.Cfg,
	}

	// Read API
	r.Get("/logs/poll", h.logsPoll)
	r.Get("/logs/histogram", h.logsHistogram)
	r.Get("/logs/{id}", h.getLogDetail)
	r.Get("/services", h.listServices)
	r.Get("/event_types", h.listEventTypes)
	r.Get("/log_values", h.logValues)

	// Page routes (mounted on the page router with auth middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/logs", h.logsPage)
		deps.PageRouter.Get("/logs/fragment", h.logsFragment)
	}
}
