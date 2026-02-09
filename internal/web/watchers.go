package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

type createWatcherRequest struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Severity    store.WatcherSeverity `json:"severity"`
	Filters     json.RawMessage      `json:"filters"`
	TimeRange   string               `json:"time_range"`
	Notify      json.RawMessage      `json:"notify"`
}

type updateWatcherRequest struct {
	Title       *string                `json:"title,omitempty"`
	Description *string                `json:"description,omitempty"`
	Severity    *store.WatcherSeverity `json:"severity,omitempty"`
	Filters     json.RawMessage        `json:"filters,omitempty"`
	TimeRange   *string                `json:"time_range,omitempty"`
	Notify      json.RawMessage        `json:"notify,omitempty"`
}

func (s *Server) handleCreateWatcher(w http.ResponseWriter, r *http.Request) {
	var req createWatcherRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" || req.Description == "" {
		writeError(w, http.StatusBadRequest, "title and description are required")
		return
	}

	watcher, err := s.watcherStore.Create(r.Context(), store.CreateWatcherParams{
		Title:       req.Title,
		Description: req.Description,
		Severity:    req.Severity,
		Filters:     req.Filters,
		TimeRange:   req.TimeRange,
		Notify:      req.Notify,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create watcher")
		return
	}
	writeJSON(w, http.StatusCreated, watcher)
}

func (s *Server) handleListWatchers(w http.ResponseWriter, r *http.Request) {
	list, err := s.watcherStore.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list watchers")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetWatcher(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid watcher ID")
		return
	}

	watcher, err := s.watcherStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get watcher")
		return
	}
	writeJSON(w, http.StatusOK, watcher)
}

func (s *Server) handleUpdateWatcher(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid watcher ID")
		return
	}

	var req updateWatcherRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	watcher, err := s.watcherStore.Update(r.Context(), id, store.UpdateWatcherParams{
		Title:       req.Title,
		Description: req.Description,
		Severity:    req.Severity,
		Filters:     req.Filters,
		TimeRange:   req.TimeRange,
		Notify:      req.Notify,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update watcher")
		return
	}
	writeJSON(w, http.StatusOK, watcher)
}

func (s *Server) handleDeleteWatcher(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid watcher ID")
		return
	}

	if err := s.watcherStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete watcher")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePauseWatcher(w http.ResponseWriter, r *http.Request) {
	s.setWatcherStatus(w, r, store.WatcherPaused)
}

func (s *Server) handleResumeWatcher(w http.ResponseWriter, r *http.Request) {
	s.setWatcherStatus(w, r, store.WatcherActive)
}

func (s *Server) setWatcherStatus(w http.ResponseWriter, r *http.Request, status store.WatcherStatus) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid watcher ID")
		return
	}

	watcher, err := s.watcherStore.UpdateStatus(r.Context(), id, status)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update watcher status")
		return
	}
	writeJSON(w, http.StatusOK, watcher)
}

func (s *Server) handleRunWatcherNow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid watcher ID")
		return
	}

	watcher, err := s.watcherStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get watcher")
		return
	}

	// Trigger immediate execution in background
	if s.executor != nil {
		go s.executor.Execute(r.Context(), *watcher)
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "triggered"})
}

func (s *Server) handleListWatcherRuns(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid watcher ID")
		return
	}

	runs, err := s.runStore.List(r.Context(), id, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleGetWatcherRun(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(chi.URLParam(r, "runId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run ID")
		return
	}

	run, err := s.runStore.GetByID(r.Context(), runID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get run")
		return
	}
	writeJSON(w, http.StatusOK, run)
}
