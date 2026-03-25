package errorimpact

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the error impact domain.
var Module = server.Module{
	Name:  "errorimpact",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		store: deps.ErrorImpactStore,
	}

	r.Get("/errors/{fingerprint}/impact", h.getImpact)
	r.Get("/errors/{fingerprint}/affected-users", h.affectedUsers)
	r.Get("/users/{userID}/errors", h.userErrors)
	r.Get("/errors/top-by-impact", h.topByImpact)
}
