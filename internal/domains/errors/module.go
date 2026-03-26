package errors

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the error groups domain.
var Module = server.Module{
	Name:  "errors",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		store:    deps.ErrorGroupStore,
		logStore: deps.LogStore,
		db:       deps.DB,
		cfg:      deps.Cfg,
	}

	// Read API
	r.Get("/errors", h.list)
	r.Get("/errors/batch", h.batch)
	r.Get("/errors/{fingerprint}", h.get)
	r.Get("/errors/{fingerprint}/histogram", h.histogram)

	// Write API
	r.Post("/errors/{fingerprint}/resolve", h.resolve)
	r.Post("/errors/{fingerprint}/ignore", h.ignore)

	// Page routes (mounted on the page router with auth middleware)
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/errors", h.errorsPage)
		deps.PageRouter.Get("/errors/{fingerprint}", h.errorDetailPage)
	}
}
