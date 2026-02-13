package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

// handleInvestigateAlert triggers a deeper follow-up investigation for an alert.
func (s *Server) handleInvestigateAlert(w http.ResponseWriter, r *http.Request) {
	if s.watcherStore == nil || s.executor == nil {
		writeError(w, http.StatusServiceUnavailable, "investigation not available")
		return
	}

	idStr := chi.URLParam(r, "id")
	alertID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert ID")
		return
	}

	alert, err := s.alertStore.GetByID(r.Context(), alertID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get alert")
		return
	}

	// Build investigation prompt
	prompt := buildInvestigationPrompt(alert)

	// Get original watcher's data_source_id if available
	var dataSourceID string
	if alert.WatcherID != nil && s.watcherStore != nil {
		origWatcher, err := s.watcherStore.GetByID(r.Context(), *alert.WatcherID)
		if err == nil {
			if origWatcher.DataSourceID != nil {
				dataSourceID = *origWatcher.DataSourceID
			}
		}
	}

	// Create a temporary investigation watcher (paused so it won't be scheduled)
	params := store.CreateWatcherParams{
		Title:       fmt.Sprintf("Investigation: %s", alert.Title),
		Description: prompt,
		Severity:    alert.Severity,
		Filters:     json.RawMessage(`{}`),
		TimeRange:   "30m",
		Model:       "",
		Effort:      store.EffortHigh,
		WatcherType: store.WatcherTypeAI,
		Notify:      json.RawMessage(`["dashboard"]`),
	}
	if dataSourceID != "" {
		params.DataSourceID = &dataSourceID
	}

	w2, err := s.watcherStore.Create(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create investigation watcher")
		return
	}

	// Pause it immediately (so the scheduler doesn't pick it up)
	s.watcherStore.UpdateStatus(r.Context(), w2.ID, store.WatcherPaused)

	// Execute immediately in background
	go func() {
		defer func() {
			if rv := recover(); rv != nil {
				slog.Error("panic in investigation watcher", "watcher_id", w2.ID, "error", rv)
			}
		}()
		s.executor.Execute(context.Background(), *w2)
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "triggered",
		"watcher_id": w2.ID.String(),
		"message":    fmt.Sprintf("Investigation started for alert '%s'. View progress at /watchers/%s/runs", alert.Title, w2.ID.String()),
	})
}

// buildInvestigationPrompt creates an investigation-focused AI prompt.
func buildInvestigationPrompt(alert *store.Alert) string {
	return fmt.Sprintf(`INVESTIGATION MODE: Deep follow-up investigation.

ORIGINAL ALERT:
Title: %s
Severity: %s
Summary: %s
Time: %s

YOUR TASK:
1. Verify if the issue is still present
2. Investigate root cause with deeper analysis
3. Check for related/cascading failures
4. Provide actionable remediation steps
5. Assess: getting worse, stable, or improving?

Use all available tools to gather evidence. Be thorough and systematic.`,
		alert.Title,
		alert.Severity,
		alert.Summary,
		alert.CreatedAt.Format("2006-01-02 15:04:05"),
	)
}
