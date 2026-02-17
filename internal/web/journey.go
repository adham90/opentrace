package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleListSessionsAPI(w http.ResponseWriter, r *http.Request) {
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

	sessions, err := s.journeyStore.ListSessions(r.Context(), store.SessionListParams{
		UserID:   q.Get("user_id"),
		Service:  q.Get("service"),
		HasError: hasError,
		Since:    since,
		Until:    time.Now().UTC(),
		Limit:    limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleGetSessionAPI(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	session, err := s.journeyStore.GetSession(r.Context(), sessionID)
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get session")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleSessionRequestsAPI(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	steps, err := s.journeyStore.GetSessionRequests(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get session requests")
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

func (s *Server) handleSessionTimelineAPI(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	timelines, err := s.journeyStore.GetSessionTimeline(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get session timeline")
		return
	}
	writeJSON(w, http.StatusOK, timelines)
}

func (s *Server) handleUserJourneyAPI(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
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

	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	steps, err := s.journeyStore.GetUserJourney(r.Context(), userID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user journey")
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

func (s *Server) handlePathAnalysisAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sinceStr := q.Get("since")
	if sinceStr == "" {
		sinceStr = "7d"
	}
	since, err := parseSinceParam(sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
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

	paths, err := s.journeyStore.CommonPaths(r.Context(), store.PathAnalysisParams{
		Service:        q.Get("service"),
		Since:          since,
		MinOccurrences: minOccurrences,
		PathLength:     pathLength,
		ErrorPathsOnly: errorPathsOnly,
		StartingFrom:   q.Get("starting_from"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to analyze paths")
		return
	}
	writeJSON(w, http.StatusOK, paths)
}

func (s *Server) handleListFunnelsAPI(w http.ResponseWriter, r *http.Request) {
	funnels, err := s.journeyStore.ListFunnels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list funnels")
		return
	}
	writeJSON(w, http.StatusOK, funnels)
}

func (s *Server) handleAnalyzeFunnelAPI(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "funnelID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid funnel ID")
		return
	}

	sinceStr := r.URL.Query().Get("since")
	if sinceStr == "" {
		sinceStr = "7d"
	}
	since, err := parseSinceParam(sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}

	result, err := s.journeyStore.AnalyzeFunnel(r.Context(), id, since)
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "funnel not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to analyze funnel")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRequestTimelineAPI(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "logID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid log ID")
		return
	}

	rt, err := s.journeyStore.GetRequestTimeline(r.Context(), id)
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get timeline")
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (s *Server) handleCreateFunnelAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string             `json:"name"`
		Service string             `json:"service"`
		Steps   []store.FunnelStep `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || len(body.Steps) == 0 {
		writeError(w, http.StatusBadRequest, "name and steps are required")
		return
	}

	funnel, err := s.journeyStore.CreateFunnel(r.Context(), store.Funnel{
		Name:    body.Name,
		Service: body.Service,
		Steps:   body.Steps,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create funnel")
		return
	}
	writeJSON(w, http.StatusCreated, funnel)
}

func (s *Server) handleDeleteFunnelAPI(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "funnelID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid funnel ID")
		return
	}

	if err := s.journeyStore.DeleteFunnel(r.Context(), id); err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "funnel not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete funnel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
