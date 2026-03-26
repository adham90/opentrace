package watches

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	"github.com/adham90/opentrace/internal/watcher"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

type handler struct {
	watchStore                store.WatchStore
	logStore                  store.LogStore
	watchMetrics              *watcher.WatchMetrics
	auditStore                store.AuditStore
	investigationSessionStore store.InvestigationSessionStore
	cfg                       *config.Config
}

// list returns active watches as JSON.
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteJSON(w, http.StatusOK, []struct{}{})
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

	watches, err := h.watchStore.List(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list watches")
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

	server.WriteJSON(w, http.StatusOK, result)
}

// get returns a single watch by ID.
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	id := chi.URLParam(r, "id")
	wt, err := h.watchStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		server.WriteError(w, http.StatusNotFound, "watch not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get watch")
		return
	}
	server.WriteJSON(w, http.StatusOK, wt)
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

// create creates a new metric watch.
func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "watch system not configured")
		return
	}

	var req createWatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Metric == "" {
		server.WriteError(w, http.StatusBadRequest, "metric is required")
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

	if user := server.UserFromContext(r.Context()); user != nil {
		params.CreatedBy = user.DisplayName
	}

	created, err := h.watchStore.Create(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create watch")
		return
	}

	// Capture baseline if we have log data.
	if h.logStore != nil && h.watchMetrics != nil {
		baseline, err := watcher.CaptureBaseline(r.Context(), h.logStore, h.watchMetrics, created)
		if err == nil && baseline != nil {
			_ = h.watchStore.UpdateBaseline(r.Context(), created.ID, baseline)
			created.BaselineJSON = baseline
		}
	}

	server.Audit(r, h.auditStore, "create", "watch", created.ID, "metric="+string(created.Metric))
	server.WriteJSON(w, http.StatusCreated, created)
}

// delete stops and deletes a watch.
func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	id := chi.URLParam(r, "id")
	err := h.watchStore.Delete(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		server.WriteError(w, http.StatusNotFound, "watch not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to delete watch")
		return
	}
	server.Audit(r, h.auditStore, "delete", "watch", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// listRuns returns runs for a specific watch.
func (h *handler) listRuns(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteJSON(w, http.StatusOK, []struct{}{})
		return
	}
	id := chi.URLParam(r, "id")
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	runs, err := h.watchStore.ListRuns(r.Context(), id, limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list watch runs")
		return
	}
	if runs == nil {
		runs = []store.WatchRun{}
	}
	server.WriteJSON(w, http.StatusOK, runs)
}

// listAlerts returns alerts for a specific watch.
func (h *handler) listAlerts(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteJSON(w, http.StatusOK, []struct{}{})
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
	alerts, err := h.watchStore.ListAlerts(r.Context(), id, status, limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list watch alerts")
		return
	}
	if alerts == nil {
		alerts = []store.WatchAlert{}
	}
	server.WriteJSON(w, http.StatusOK, alerts)
}

// getAlert returns a single watch alert with evidence.
func (h *handler) getAlert(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	alertID := chi.URLParam(r, "alertId")
	alert, err := h.watchStore.GetAlert(r.Context(), alertID)
	if err != nil {
		server.WriteError(w, http.StatusNotFound, "alert not found")
		return
	}
	server.WriteJSON(w, http.StatusOK, alert)
}

// dismissAlert dismisses a watch alert with a reason.
func (h *handler) dismissAlert(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	alertID := chi.URLParam(r, "alertId")
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		json.NewDecoder(r.Body).Decode(&body)
	}
	if err := h.watchStore.DismissAlert(r.Context(), alertID, body.Reason); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to dismiss alert")
		return
	}
	server.Audit(r, h.auditStore, "dismiss", "watch_alert", alertID, body.Reason)
	w.WriteHeader(http.StatusNoContent)
}

// acknowledgeAlert marks a watch alert as acknowledged.
func (h *handler) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteError(w, http.StatusNotFound, "watch system not configured")
		return
	}
	alertID := chi.URLParam(r, "alertId")
	if err := h.watchStore.AcknowledgeAlert(r.Context(), alertID); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to acknowledge alert")
		return
	}
	server.Audit(r, h.auditStore, "acknowledge", "watch_alert", alertID, "")
	w.WriteHeader(http.StatusNoContent)
}

// alertCount returns the count of pending watch alerts.
func (h *handler) alertCount(w http.ResponseWriter, r *http.Request) {
	if h.watchStore == nil {
		server.WriteJSON(w, http.StatusOK, map[string]int{"count": 0})
		return
	}
	count, err := h.watchStore.CountPendingAlerts(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to count alerts")
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]int{"count": count})
}

// ── Page handlers ────────────────────────────────────────────

func (h *handler) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := server.UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
		DevMode: h.cfg != nil && h.cfg.DevMode,
	}
}

// watchersPage renders the watchers listing page.
func (h *handler) watchersPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Watchers", "watchers")
	var watches []store.Watch
	var watchAlerts []store.WatchAlert
	var pendingCount int
	if h.watchStore != nil {
		w2, err := h.watchStore.List(r.Context(), store.ListWatchParams{Limit: 100})
		if err == nil {
			watches = w2
		}
		a, err := h.watchStore.ListAlerts(r.Context(), "", "", 20)
		if err == nil {
			watchAlerts = a
		}
		p, err := h.watchStore.CountPendingAlerts(r.Context())
		if err == nil {
			pendingCount = p
		}
	}
	webviews.WatchersPage(layout, watches, watchAlerts, pendingCount).Render(r.Context(), w)
}

// watchDetailPage renders the watcher detail page.
func (h *handler) watchDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Redirect(w, r, "/watchers", http.StatusFound)
		return
	}
	if h.watchStore == nil {
		server.WriteError(w, http.StatusNotFound, "watch system not configured")
		return
	}

	wt, err := h.watchStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		server.WriteError(w, http.StatusNotFound, "watch not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get watch")
		return
	}

	layout := h.layoutData(r, "Watch: "+string(wt.Metric), "watchers")

	detail := webviews.WatcherDetailData{
		Watch: wt,
	}

	// Fetch execution runs
	runs, err := h.watchStore.ListRuns(r.Context(), id, 50)
	if err == nil {
		detail.Runs = runs
	}

	// Fetch alerts for this watch
	alerts, err := h.watchStore.ListAlerts(r.Context(), id, "", 20)
	if err == nil {
		detail.Alerts = alerts
	}

	// Look up the investigation session that created this watch
	if wt.SessionID != "" && h.investigationSessionStore != nil {
		sess, err := h.investigationSessionStore.GetByID(r.Context(), wt.SessionID)
		if err == nil {
			detail.Session = sess
		}
	}

	webviews.WatcherDetailPage(layout, detail).Render(r.Context(), w)
}
