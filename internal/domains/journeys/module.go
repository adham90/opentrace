package journeys

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the user journeys domain.
var Module = server.Module{
	Name:  "journeys",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		store: deps.JourneyStore,
	}

	r.Get("/sessions", h.listSessions)
	r.Get("/sessions/{sessionID}", h.getSession)
	r.Get("/sessions/{sessionID}/requests", h.sessionRequests)
	r.Get("/sessions/{sessionID}/timeline", h.sessionTimeline)
	r.Get("/users/{userID}/journey", h.userJourney)
	r.Get("/paths/analysis", h.pathAnalysis)
	r.Get("/funnels", h.listFunnels)
	r.Post("/funnels", h.createFunnel)
	r.Get("/funnels/{funnelID}/analyze", h.analyzeFunnel)
	r.Delete("/funnels/{funnelID}", h.deleteFunnel)
	r.Get("/requests/{logID}/timeline", h.requestTimeline)
}
