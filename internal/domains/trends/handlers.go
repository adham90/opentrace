package trends

import (
	"net/http"
	"strconv"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type handler struct {
	trendStore     store.TrendStore
	analyticsStore store.AnalyticsStore
}

func (h *handler) trends(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	interval := q.Get("interval")
	if interval == "" {
		interval = "1h"
	}

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}

	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	until := time.Now().UTC()
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = t
		}
	}

	params := store.TrendQueryParams{
		Service:     q.Get("service"),
		Endpoint:    q.Get("endpoint"),
		Environment: q.Get("environment"),
		Interval:    interval,
		Metric:      q.Get("metric"),
		Since:       since,
		Until:       until,
	}

	buckets, err := h.trendStore.QueryTrends(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to query trends")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"buckets": buckets,
		"params":  params,
	})
}

func (h *handler) deployMarkers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	markers, err := h.trendStore.ListDeployMarkers(r.Context(), q.Get("service"), since)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list deploy markers")
		return
	}
	server.WriteJSON(w, http.StatusOK, markers)
}

func (h *handler) analyticsSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	summary, err := h.analyticsStore.TrafficSummary(r.Context(), store.AnalyticsParams{
		Service: q.Get("service"),
		Since:   since,
		Until:   time.Now().UTC(),
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get traffic summary")
		return
	}
	server.WriteJSON(w, http.StatusOK, summary)
}

func (h *handler) topEndpoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	limit := 20
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	sortBy := q.Get("sort_by")
	if sortBy == "" {
		sortBy = "request_count"
	}

	endpoints, err := h.analyticsStore.TopEndpoints(r.Context(), store.TopEndpointParams{
		Service:     q.Get("service"),
		Since:       since,
		Until:       time.Now().UTC(),
		SortBy:      sortBy,
		Limit:       limit,
		MinRequests: 1,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get top endpoints")
		return
	}
	server.WriteJSON(w, http.StatusOK, endpoints)
}

func (h *handler) trafficHeatmap(w http.ResponseWriter, r *http.Request) {
	cells, err := h.analyticsStore.TrafficHeatmap(r.Context(), r.URL.Query().Get("service"))
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get heatmap")
		return
	}
	server.WriteJSON(w, http.StatusOK, cells)
}
