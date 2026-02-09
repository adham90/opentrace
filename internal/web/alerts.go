package web

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
)

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	params := store.ListAlertParams{
		Limit: 50,
	}

	if r.URL.Query().Get("unread") == "true" {
		params.UnreadOnly = true
	}

	if wID := r.URL.Query().Get("watcher_id"); wID != "" {
		id, err := uuid.Parse(wID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid watcher_id")
			return
		}
		params.WatcherID = &id
	}

	alerts, err := s.alertStore.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alerts")
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (s *Server) handleAlertCount(w http.ResponseWriter, r *http.Request) {
	count, err := s.alertStore.CountUnread(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count alerts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

func (s *Server) handleMarkAlertRead(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert ID")
		return
	}

	if err := s.alertStore.MarkRead(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to mark alert read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDismissAlert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert ID")
		return
	}

	if err := s.alertStore.Dismiss(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to dismiss alert")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
