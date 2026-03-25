package deploys

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the deploys domain.
var Module = server.Module{
	Name:  "deploys",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		store: deps.DeployStore,
	}

	r.Post("/events/deploy", h.webhook)
	r.Get("/deploys", h.list)
}
