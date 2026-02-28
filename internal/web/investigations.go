package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

// handleListInvestigations returns a paginated list of investigation sessions.
func (s *Server) handleListInvestigations(w http.ResponseWriter, r *http.Request) {
	if s.investigationSessionStore == nil {
		writeError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	params := store.ListInvestigationSessionParams{}

	if v := r.URL.Query().Get("status"); v != "" {
		params.Status = store.InvestigationSessionStatus(v)
	}
	if v := r.URL.Query().Get("intent"); v != "" {
		params.Intent = v
	}
	if v := r.URL.Query().Get("service"); v != "" {
		params.Service = v
	}
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			params.Since = t
		}
	}
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

	sessions, err := s.investigationSessionStore.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list investigation sessions")
		return
	}

	writeJSON(w, http.StatusOK, sessions)
}

// handleGetInvestigation returns a single investigation session by ID.
func (s *Server) handleGetInvestigation(w http.ResponseWriter, r *http.Request) {
	if s.investigationSessionStore == nil {
		writeError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	id := chi.URLParam(r, "id")
	sess, err := s.investigationSessionStore.GetByID(r.Context(), id)
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "investigation session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get investigation session")
		return
	}

	writeJSON(w, http.StatusOK, sess)
}

// handleInvestigationStats returns aggregate statistics about investigation sessions.
func (s *Server) handleInvestigationStats(w http.ResponseWriter, r *http.Request) {
	if s.investigationSessionStore == nil {
		writeError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	stats, err := s.investigationSessionStore.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get investigation stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// handleInvestigationSteps returns the MCP activity events for a given investigation session.
func (s *Server) handleInvestigationSteps(w http.ResponseWriter, r *http.Request) {
	if s.investigationSessionStore == nil || s.mcpActivityStore == nil {
		writeError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	id := chi.URLParam(r, "id")

	// Verify session exists
	_, err := s.investigationSessionStore.GetByID(r.Context(), id)
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "investigation session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get investigation session")
		return
	}

	events, err := s.mcpActivityStore.ListByInvestigationSession(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list investigation steps")
		return
	}

	writeJSON(w, http.StatusOK, events)
}
