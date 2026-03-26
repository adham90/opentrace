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

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		trendStore:     deps.TrendStore,
		analyticsStore: deps.AnalyticsStore,
	}

	r.Get("/trends", h.trends)
	r.Get("/trends/deploy-markers", h.deployMarkers)
	r.Get("/analytics/summary", h.analyticsSummary)
	r.Get("/analytics/top-endpoints", h.topEndpoints)
	r.Get("/analytics/heatmap", h.trafficHeatmap)
}
