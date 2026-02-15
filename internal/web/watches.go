package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// handleWatchesPage renders the watches UI page.
func (s *Server) handleWatchesPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Watches", "watches")
	tmpl := s.getTemplate(watchesTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/watches.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

// handleListWatches returns active watches as JSON.
func (s *Server) handleListWatches(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}

	params := store.ListWatchParams{}
	if v := r.URL.Query().Get("status"); v != "" {
		params.Status = store.WatchStatus(v)
	}
	if v := r.URL.Query().Get("service"); v != "" {
		params.Service = v
	}
	if v := r.URL.Query().Get("session_id"); v != "" {
		params.SessionID = v
	}

	watches, err := s.watchStore.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list watches")
		return
	}
	if watches == nil {
		watches = []store.Watch{}
	}

	// Enrich with current metric values for active watches.
	type watchWithMetrics struct {
		store.Watch
		CurrentValue *float64 `json:"current_value,omitempty"`
		ExpiresIn    string   `json:"expires_in,omitempty"`
	}
	result := make([]watchWithMetrics, len(watches))
	for i, wt := range watches {
		result[i] = watchWithMetrics{Watch: wt}
		if wt.ExpiresAt != nil && wt.ExpiresAt.After(time.Now()) {
			d := time.Until(*wt.ExpiresAt)
			if d > time.Hour {
				result[i].ExpiresIn = strconv.Itoa(int(d.Hours())) + "h " + strconv.Itoa(int(d.Minutes())%60) + "m"
			} else {
				result[i].ExpiresIn = strconv.Itoa(int(d.Minutes())) + "m"
			}
		}
		if wt.CurrentValue != nil {
			result[i].CurrentValue = wt.CurrentValue
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// handleGetWatch returns a single watch by ID.
func (s *Server) handleGetWatch(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	id := chi.URLParam(r, "id")
	wt, err := s.watchStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "watch not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get watch")
		return
	}
	writeJSON(w, http.StatusOK, wt)
}

type createWatchRequest struct {
	Metric         string  `json:"metric"`
	Operator       string  `json:"operator"`
	Threshold      float64 `json:"threshold"`
	Service        string  `json:"service"`
	Endpoint       string  `json:"endpoint"`
	Environment    string  `json:"environment"`
	CommitHash     string  `json:"commit_hash"`
	Duration       string  `json:"duration"`
	Urgency        string  `json:"urgency"`
	CheckInterval  string  `json:"check_interval"`
	BaselineWindow string  `json:"baseline_window"`
	MinConsecutive int     `json:"min_consecutive"`
	SessionID      string  `json:"session_id"`
}

// handleCreateWatch creates a new metric watch.
func (s *Server) handleCreateWatch(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeError(w, http.StatusInternalServerError, "watch system not configured")
		return
	}

	var req createWatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Metric == "" {
		writeError(w, http.StatusBadRequest, "metric is required")
		return
	}

	params := store.CreateWatchParams{
		Metric:         store.WatchMetric(req.Metric),
		Operator:       store.WatchOperator(req.Operator),
		Threshold:      req.Threshold,
		Service:        req.Service,
		Endpoint:       req.Endpoint,
		Environment:    req.Environment,
		CommitHash:     req.CommitHash,
		Duration:       req.Duration,
		Urgency:        store.WatchUrgency(req.Urgency),
		CheckInterval:  req.CheckInterval,
		BaselineWindow: req.BaselineWindow,
		MinConsecutive: req.MinConsecutive,
		SessionID:      req.SessionID,
	}

	if user := UserFromContext(r.Context()); user != nil {
		params.CreatedBy = user.DisplayName
	}

	created, err := s.watchStore.Create(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create watch")
		return
	}

	// Capture baseline if we have log data.
	if s.logStore != nil && s.watchMetrics != nil {
		baseline, err := watcher.CaptureBaseline(r.Context(), s.logStore, s.watchMetrics, created)
		if err == nil && baseline != nil {
			_ = s.watchStore.UpdateBaseline(r.Context(), created.ID, baseline)
			created.BaselineJSON = baseline
		}
	}

	s.audit(r, "create", "watch", created.ID, "metric="+string(created.Metric))
	writeJSON(w, http.StatusCreated, created)
}

// handleDeleteWatch stops and deletes a watch.
func (s *Server) handleDeleteWatch(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	id := chi.URLParam(r, "id")
	err := s.watchStore.Delete(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "watch not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete watch")
		return
	}
	s.audit(r, "delete", "watch", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// handleListWatchRuns returns runs for a specific watch.
func (s *Server) handleListWatchRuns(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	id := chi.URLParam(r, "id")
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	runs, err := s.watchStore.ListRuns(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list watch runs")
		return
	}
	if runs == nil {
		runs = []store.WatchRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleListWatchAlerts returns alerts for a specific watch.
func (s *Server) handleListWatchAlerts(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	id := chi.URLParam(r, "id")
	status := r.URL.Query().Get("status")
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	alerts, err := s.watchStore.ListAlerts(r.Context(), id, status, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list watch alerts")
		return
	}
	if alerts == nil {
		alerts = []store.WatchAlert{}
	}
	writeJSON(w, http.StatusOK, alerts)
}

// handleGetWatchAlert returns a single watch alert with evidence.
func (s *Server) handleGetWatchAlert(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	alertID := chi.URLParam(r, "alertId")
	alert, err := s.watchStore.GetAlert(r.Context(), alertID)
	if err != nil {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	writeJSON(w, http.StatusOK, alert)
}

// handleDismissWatchAlert dismisses a watch alert with a reason.
func (s *Server) handleDismissWatchAlert(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	alertID := chi.URLParam(r, "alertId")
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		json.NewDecoder(r.Body).Decode(&body)
	}
	if err := s.watchStore.DismissAlert(r.Context(), alertID, body.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to dismiss alert")
		return
	}
	s.audit(r, "dismiss", "watch_alert", alertID, body.Reason)
	w.WriteHeader(http.StatusNoContent)
}

// handleAcknowledgeWatchAlert marks a watch alert as acknowledged.
func (s *Server) handleAcknowledgeWatchAlert(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	alertID := chi.URLParam(r, "alertId")
	if err := s.watchStore.AcknowledgeAlert(r.Context(), alertID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to acknowledge alert")
		return
	}
	s.audit(r, "acknowledge", "watch_alert", alertID, "")
	w.WriteHeader(http.StatusNoContent)
}

// handleWatchAlertCount returns the count of pending watch alerts.
func (s *Server) handleWatchAlertCount(w http.ResponseWriter, r *http.Request) {
	if s.watchStore == nil {
		writeJSON(w, http.StatusOK, map[string]int{"count": 0})
		return
	}
	count, err := s.watchStore.CountPendingAlerts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count alerts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}
