package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleListHealthChecks(w http.ResponseWriter, r *http.Request) {
	params := store.ListHealthCheckParams{}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			params.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			params.Offset = n
		}
	}
	checks, err := s.healthCheckStore.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list health checks")
		return
	}
	if checks == nil {
		checks = []store.HealthCheck{}
	}
	writeJSON(w, http.StatusOK, checks)
}

func (s *Server) handleHealthCheckResults(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	results, err := s.healthCheckStore.LatestResults(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get health check results")
		return
	}
	if results == nil {
		results = []store.HealthCheckResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleUptimeSummary(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil && v > 0 {
			hours = v
		}
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	summaries, err := s.healthCheckStore.UptimeSummaries(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get uptime summaries")
		return
	}
	if summaries == nil {
		summaries = []store.UptimeSummary{}
	}

	// Enrich with reliability data from the in-memory rolling window.
	if s.reliabilityProvider != nil {
		for i := range summaries {
			summaries[i].Reliability = s.reliabilityProvider.Reliability(summaries[i].HealthCheckID)
		}
	}

	writeJSON(w, http.StatusOK, summaries)
}

func (s *Server) handleCreateHealthCheck(w http.ResponseWriter, r *http.Request) {
	var params store.CreateHealthCheckParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if params.Name == "" || params.URL == "" {
		writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}

	hc, err := s.healthCheckStore.Create(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create health check")
		return
	}
	writeJSON(w, http.StatusCreated, hc)
}

func (s *Server) handleDeleteHealthCheck(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.healthCheckStore.Delete(r.Context(), id); err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "health check not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete health check")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
