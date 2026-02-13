package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

// handleListAlertGroups lists alert groups with optional status/environment filters.
func (s *Server) handleListAlertGroups(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")

	groups, err := s.alertGroupStore.List(r.Context(), status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alert groups")
		return
	}

	writeJSON(w, http.StatusOK, groups)
}

// handleGetAlertGroup returns a single alert group with its member alerts.
func (s *Server) handleGetAlertGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	group, err := s.alertGroupStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert group not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get alert group")
		return
	}

	writeJSON(w, http.StatusOK, group)
}

// handleCreateAlertGroup creates a new alert group with selected alerts.
func (s *Server) handleCreateAlertGroup(w http.ResponseWriter, r *http.Request) {
	var params store.CreateAlertGroupParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if params.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	group, err := s.alertGroupStore.Create(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create alert group")
		return
	}

	writeJSON(w, http.StatusCreated, group)
}

// handleUpdateAlertGroup updates an alert group's status, root cause, etc.
func (s *Server) handleUpdateAlertGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var params store.UpdateAlertGroupParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.alertGroupStore.Update(r.Context(), id, params); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert group not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update alert group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleAddAlertsToGroup adds alerts to an existing group.
func (s *Server) handleAddAlertsToGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		AlertIDs []string `json:"alert_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.alertGroupStore.AddAlerts(r.Context(), id, body.AlertIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add alerts to group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

// handleDeleteAlertGroup deletes an alert group.
func (s *Server) handleDeleteAlertGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.alertGroupStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert group not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete alert group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleAutoCorrelate triggers automatic alert correlation.
func (s *Server) handleAutoCorrelate(w http.ResponseWriter, r *http.Request) {
	windowMinutes := 10
	if v := r.URL.Query().Get("window"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			windowMinutes = n
		}
	}

	groups, err := s.alertGroupStore.AutoCorrelate(r.Context(), windowMinutes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to auto-correlate alerts")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"groups_created": len(groups),
		"groups":         groups,
	})
}
