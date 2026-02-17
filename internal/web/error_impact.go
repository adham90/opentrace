package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleErrorImpactAPI(w http.ResponseWriter, r *http.Request) {
	fingerprint := chi.URLParam(r, "fingerprint")
	if fingerprint == "" {
		writeError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}

	impact, err := s.errorImpactStore.GetImpact(r.Context(), fingerprint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get impact")
		return
	}

	writeJSON(w, http.StatusOK, impact)
}

func (s *Server) handleAffectedUsersAPI(w http.ResponseWriter, r *http.Request) {
	fingerprint := chi.URLParam(r, "fingerprint")
	if fingerprint == "" {
		writeError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	users, err := s.errorImpactStore.GetAffectedUsers(r.Context(), fingerprint, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get affected users")
		return
	}

	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleUserErrorsAPI(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userID is required")
		return
	}

	sinceStr := r.URL.Query().Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := parseSinceParam(sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	errors, err := s.errorImpactStore.GetUserErrors(r.Context(), userID, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user errors")
		return
	}

	writeJSON(w, http.StatusOK, errors)
}

func (s *Server) handleTopErrorsByImpactAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	params := store.ImpactQueryParams{
		Limit: 20,
	}

	if v := q.Get("status"); v != "" {
		params.Status = store.ErrorGroupStatus(v)
	}
	if v := q.Get("service"); v != "" {
		params.Service = v
	}
	if v := q.Get("sort_by"); v != "" {
		params.SortBy = v
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			params.Limit = n
		}
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
	params.Since = since

	results, err := s.errorImpactStore.TopByImpact(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get top errors by impact")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"since":   since.Format(time.RFC3339),
		"results": results,
	})
}
