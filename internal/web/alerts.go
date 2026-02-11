package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	params := store.ListAlertParams{
		Limit: 50,
	}

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			params.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			params.Offset = n
		}
	}

	if env := r.URL.Query().Get("env"); env != "" {
		params.Environment = env
	}

	if r.URL.Query().Get("dismissed") == "true" {
		params.DismissedOnly = true
	} else if r.URL.Query().Get("unread") == "true" {
		params.UnreadOnly = true
	}

	if sev := r.URL.Query().Get("severity"); sev != "" {
		params.Severity = store.WatcherSeverity(sev)
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
	env := r.URL.Query().Get("env")
	unread, err := s.alertStore.CountUnread(r.Context(), env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count alerts")
		return
	}
	total, err := s.alertStore.CountTotal(r.Context(), env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count alerts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": unread, "total": total})
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

func (s *Server) handleMarkAllAlertsRead(w http.ResponseWriter, r *http.Request) {
	env := r.URL.Query().Get("env")
	if err := s.alertStore.MarkAllRead(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark all alerts read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDismissAllAlerts(w http.ResponseWriter, r *http.Request) {
	env := r.URL.Query().Get("env")
	if err := s.alertStore.DismissAll(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to dismiss all alerts")
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
