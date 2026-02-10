package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

type createWatcherRequest struct {
	Title        string               `json:"title"`
	Description  string               `json:"description"`
	Environment  string               `json:"environment"`
	Severity     store.WatcherSeverity `json:"severity"`
	Filters      json.RawMessage      `json:"filters"`
	TimeRange    string               `json:"time_range"`
	Schedule     string               `json:"schedule,omitempty"`
	Model        string               `json:"model"`
	Effort       store.WatcherEffort   `json:"effort"`
	Notify       json.RawMessage      `json:"notify"`
	MonitorType  store.MonitorType     `json:"monitor_type"`
	RuleConfig   *store.RuleConfig     `json:"rule_config,omitempty"`
	DataSourceID *string               `json:"data_source_id,omitempty"`
}

type updateWatcherRequest struct {
	Title        *string                `json:"title,omitempty"`
	Description  *string                `json:"description,omitempty"`
	Environment  *string                `json:"environment,omitempty"`
	Severity     *store.WatcherSeverity `json:"severity,omitempty"`
	Filters      json.RawMessage        `json:"filters,omitempty"`
	TimeRange    *string                `json:"time_range,omitempty"`
	Schedule     *string                `json:"schedule,omitempty"`
	Model        *string                `json:"model,omitempty"`
	Effort       *store.WatcherEffort   `json:"effort,omitempty"`
	Notify       json.RawMessage        `json:"notify,omitempty"`
	MonitorType  *store.MonitorType     `json:"monitor_type,omitempty"`
	RuleConfig   *store.RuleConfig      `json:"rule_config,omitempty"`
	DataSourceID *string                `json:"data_source_id,omitempty"`
}

func (s *Server) handleCreateWatcher(w http.ResponseWriter, r *http.Request) {
	var req createWatcherRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	// AI monitors require a description; rule monitors don't
	if req.MonitorType != store.MonitorTypeRule && req.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required for AI monitors")
		return
	}

	// Validate schedule expression if provided
	if req.Schedule != "" {
		if _, err := watcher.ParseSchedule(req.Schedule); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid schedule: %v", err))
			return
		}
	}

	result, err := s.watcherStore.Create(r.Context(), store.CreateWatcherParams{
		Title:        req.Title,
		Description:  req.Description,
		Environment:  req.Environment,
		Severity:     req.Severity,
		Filters:      req.Filters,
		TimeRange:    req.TimeRange,
		Schedule:     req.Schedule,
		Model:        req.Model,
		Effort:       req.Effort,
		Notify:       req.Notify,
		MonitorType:  req.MonitorType,
		RuleConfig:   req.RuleConfig,
		DataSourceID: req.DataSourceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create watcher")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleListWatchers(w http.ResponseWriter, r *http.Request) {
	env := r.URL.Query().Get("env")
	list, err := s.watcherStore.List(r.Context(), store.ListWatcherParams{Environment: env})
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

	mon, err := s.watcherStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get watcher")
		return
	}
	writeJSON(w, http.StatusOK, mon)
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

	// Validate schedule expression if provided
	if req.Schedule != nil && *req.Schedule != "" {
		if _, err := watcher.ParseSchedule(*req.Schedule); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid schedule: %v", err))
			return
		}
	}

	updated, err := s.watcherStore.Update(r.Context(), id, store.UpdateWatcherParams{
		Title:        req.Title,
		Description:  req.Description,
		Environment:  req.Environment,
		Severity:     req.Severity,
		Filters:      req.Filters,
		TimeRange:    req.TimeRange,
		Schedule:     req.Schedule,
		Model:        req.Model,
		Effort:       req.Effort,
		Notify:       req.Notify,
		MonitorType:  req.MonitorType,
		RuleConfig:   req.RuleConfig,
		DataSourceID: req.DataSourceID,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update watcher")
		return
	}
	writeJSON(w, http.StatusOK, updated)
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

	mon, err := s.watcherStore.UpdateStatus(r.Context(), id, status)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update watcher status")
		return
	}
	writeJSON(w, http.StatusOK, mon)
}

func (s *Server) handleRunWatcherNow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid watcher ID")
		return
	}

	mon, err := s.watcherStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watcher not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get watcher")
		return
	}

	// Trigger immediate execution in background.
	// Use context.Background() — r.Context() is cancelled when the HTTP response is sent.
	if s.executor != nil {
		go s.executor.Execute(context.Background(), *mon)
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

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	var models []llm.ProviderInfo
	if s.modelRegistry != nil {
		models = s.modelRegistry.ListModels(r.Context())
	} else {
		models = llm.AvailableProviders(s.cfg)
	}
	writeJSON(w, http.StatusOK, models)
}

// handleStopRun cancels a running watcher execution.
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(chi.URLParam(r, "runId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run ID")
		return
	}

	if s.eventHub == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	if !s.eventHub.Cancel(runID) {
		writeError(w, http.StatusNotFound, "run not running or not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// handleRunEvents streams run events via SSE for live trace viewing.
func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(chi.URLParam(r, "runId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run ID")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Try to subscribe to live events
	if s.eventHub != nil {
		ch, buffered, unsub, found := s.eventHub.Subscribe(runID)
		if found {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")

			// Send buffered events for catch-up
			for _, ev := range buffered {
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			flusher.Flush()

			defer unsub()

			// Stream new events
			for {
				select {
				case ev, open := <-ch:
					if !open {
						// Run complete
						fmt.Fprintf(w, "event: done\ndata: {}\n\n")
						flusher.Flush()
						return
					}
					data, _ := json.Marshal(ev)
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
	}

	// Run not in hub — check if it has stored trace details
	run, err := s.runStore.GetByID(r.Context(), runID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get run")
		}
		return
	}

	// Return stored events as SSE (all at once) + done
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if len(run.Details) > 0 {
		// Details is a JSON array of RunEvent
		var events []json.RawMessage
		if json.Unmarshal(run.Details, &events) == nil {
			for _, ev := range events {
				fmt.Fprintf(w, "data: %s\n\n", ev)
			}
		}
	}
	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}
