package web

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

// handleAlertTrace returns the investigation trace for an alert.
// It looks up the alert, finds the linked watcher run, and returns the run details.
func (s *Server) handleAlertTrace(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert ID")
		return
	}

	alert, err := s.alertStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get alert")
		return
	}

	resp := map[string]any{
		"alert": alert,
	}

	// Get the linked watcher run if it exists
	if alert.RunID != nil && s.runStore != nil {
		run, err := s.runStore.GetByID(r.Context(), *alert.RunID)
		if err == nil {
			resp["run"] = run
		}
	}

	// Get the watcher context if it exists
	if alert.WatcherID != nil && s.watcherStore != nil {
		watcher, err := s.watcherStore.GetByID(r.Context(), *alert.WatcherID)
		if err == nil {
			resp["watcher"] = watcher
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
