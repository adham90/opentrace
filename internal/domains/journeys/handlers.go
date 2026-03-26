package journeys

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

type handler struct {
	store store.JourneyStore
}

func (h *handler) listSessions(w http.ResponseWriter, r *http.Request) {
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

	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	var hasError *bool
	if v := q.Get("has_error"); v != "" {
		b := v == "true" || v == "1"
		hasError = &b
	}

	sessions, err := h.store.ListSessions(r.Context(), store.SessionListParams{
		UserID:   q.Get("user_id"),
		Service:  q.Get("service"),
		HasError: hasError,
		Since:    since,
		Until:    time.Now().UTC(),
		Limit:    limit,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	server.WriteJSON(w, http.StatusOK, sessions)
}

func (h *handler) getSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	session, err := h.store.GetSession(r.Context(), sessionID)
	if err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get session")
		return
	}
	server.WriteJSON(w, http.StatusOK, session)
}

func (h *handler) sessionRequests(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	steps, err := h.store.GetSessionRequests(r.Context(), sessionID)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get session requests")
		return
	}
	server.WriteJSON(w, http.StatusOK, steps)
}

func (h *handler) sessionTimeline(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	timelines, err := h.store.GetSessionTimeline(r.Context(), sessionID)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get session timeline")
		return
	}
	server.WriteJSON(w, http.StatusOK, timelines)
}

func (h *handler) userJourney(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
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

	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	steps, err := h.store.GetUserJourney(r.Context(), userID, since, limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get user journey")
		return
	}
	server.WriteJSON(w, http.StatusOK, steps)
}

func (h *handler) pathAnalysis(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "7d"
	}
	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	minOccurrences := 5
	if v := q.Get("min_occurrences"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minOccurrences = n
		}
	}

	pathLength := 5
	if v := q.Get("path_length"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pathLength = n
		}
	}

	errorPathsOnly := q.Get("error_paths_only") == "true"

	paths, err := h.store.CommonPaths(r.Context(), store.PathAnalysisParams{
		Service:        q.Get("service"),
		Since:          since,
		MinOccurrences: minOccurrences,
		PathLength:     pathLength,
		ErrorPathsOnly: errorPathsOnly,
		StartingFrom:   q.Get("starting_from"),
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to analyze paths")
		return
	}
	server.WriteJSON(w, http.StatusOK, paths)
}

func (h *handler) listFunnels(w http.ResponseWriter, r *http.Request) {
	funnels, err := h.store.ListFunnels(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list funnels")
		return
	}
	server.WriteJSON(w, http.StatusOK, funnels)
}

func (h *handler) analyzeFunnel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "funnelID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid funnel ID")
		return
	}

	sinceStr := r.URL.Query().Get("since")
	if sinceStr == "" {
		sinceStr = "7d"
	}
	since, err := server.ParseSinceParam(sinceStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	result, err := h.store.AnalyzeFunnel(r.Context(), id, since)
	if err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "funnel not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to analyze funnel")
		return
	}
	server.WriteJSON(w, http.StatusOK, result)
}

func (h *handler) requestTimeline(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "logID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid log ID")
		return
	}

	rt, err := h.store.GetRequestTimeline(r.Context(), id)
	if err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "request not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get timeline")
		return
	}
	server.WriteJSON(w, http.StatusOK, rt)
}

func (h *handler) createFunnel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string             `json:"name"`
		Service string             `json:"service"`
		Steps   []store.FunnelStep `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || len(body.Steps) == 0 {
		server.WriteError(w, http.StatusBadRequest, "name and steps are required")
		return
	}

	funnel, err := h.store.CreateFunnel(r.Context(), store.Funnel{
		Name:    body.Name,
		Service: body.Service,
		Steps:   body.Steps,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create funnel")
		return
	}
	server.WriteJSON(w, http.StatusCreated, funnel)
}

func (h *handler) deleteFunnel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "funnelID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid funnel ID")
		return
	}

	if err := h.store.DeleteFunnel(r.Context(), id); err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "funnel not found")
		return
	} else if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to delete funnel")
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
