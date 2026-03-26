package traces

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the traces domain.
var Module = server.Module{
	Name:  "traces",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		store: deps.TraceStore,
	}

	r.Get("/traces", h.listRecent)
	r.Get("/traces/{traceID}", h.getStatus)
}
