package errorimpact

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type handler struct {
	store store.ErrorImpactStore
}

func (h *handler) getImpact(w http.ResponseWriter, r *http.Request) {
	fingerprint := chi.URLParam(r, "fingerprint")
	if fingerprint == "" {
		server.WriteError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}

	impact, err := h.store.GetImpact(r.Context(), fingerprint)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get impact")
		return
	}

	server.WriteJSON(w, http.StatusOK, impact)
}

func (h *handler) affectedUsers(w http.ResponseWriter, r *http.Request) {
	fingerprint := chi.URLParam(r, "fingerprint")
	if fingerprint == "" {
		server.WriteError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	users, err := h.store.GetAffectedUsers(r.Context(), fingerprint, limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get affected users")
		return
	}

	server.WriteJSON(w, http.StatusOK, users)
}

func (h *handler) userErrors(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if userID == "" {
		server.WriteError(w, http.StatusBadRequest, "userID is required")
		return
	}

	sinceStr := r.URL.Query().Get("since")
	if sinceStr == "" {
		sinceStr = "24h"
	}
	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	errors, err := h.store.GetUserErrors(r.Context(), userID, since)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get user errors")
		return
	}

	server.WriteJSON(w, http.StatusOK, errors)
}

func (h *handler) topByImpact(w http.ResponseWriter, r *http.Request) {
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
	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}
	params.Since = since

	results, err := h.store.TopByImpact(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get top errors by impact")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"since":   since.Format(time.RFC3339),
		"results": results,
	})
}
