package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleTrendsAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	interval := q.Get("interval")
	if interval == "" {
		interval = "1h"
	}

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}

	since, err := parseSinceParam(sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
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

	buckets, err := s.trendStore.QueryTrends(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query trends")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"buckets": buckets,
		"params":  params,
	})
}

func (s *Server) handleDeployMarkersAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := parseSinceParam(sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	markers, err := s.trendStore.ListDeployMarkers(r.Context(), q.Get("service"), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deploy markers")
		return
	}
	writeJSON(w, http.StatusOK, markers)
}

func (s *Server) handleAnalyticsSummaryAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := parseSinceParam(sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	summary, err := s.analyticsStore.TrafficSummary(r.Context(), store.AnalyticsParams{
		Service: q.Get("service"),
		Since:   since,
		Until:   time.Now().UTC(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get traffic summary")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleTopEndpointsAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := parseSinceParam(sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
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

	endpoints, err := s.analyticsStore.TopEndpoints(r.Context(), store.TopEndpointParams{
		Service:     q.Get("service"),
		Since:       since,
		Until:       time.Now().UTC(),
		SortBy:      sortBy,
		Limit:       limit,
		MinRequests: 1,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get top endpoints")
		return
	}
	writeJSON(w, http.StatusOK, endpoints)
}

func (s *Server) handleTrafficHeatmapAPI(w http.ResponseWriter, r *http.Request) {
	cells, err := s.analyticsStore.TrafficHeatmap(r.Context(), r.URL.Query().Get("service"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get heatmap")
		return
	}
	writeJSON(w, http.StatusOK, cells)
}

// parseSinceParam parses a duration string like "24h", "7d" into a time.Time.
func parseSinceParam(s string) (time.Time, error) {
	// Try as RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try as duration
	d, err := parseDurationWithDays(s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().UTC().Add(-d), nil
}

// parseDurationWithDays extends time.ParseDuration to support "d" suffix for days.
func parseDurationWithDays(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}
	// Handle "d" suffix
	if len(s) > 1 && s[len(s)-1] == 'd' {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err == nil {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return 0, err
}
