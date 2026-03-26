package healthchecks

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

type handler struct {
	store store.HealthCheckStore
	cfg   *config.Config
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	params := store.ListHealthCheckParams{}
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
	checks, err := h.store.List(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list health checks")
		return
	}
	if checks == nil {
		checks = []store.HealthCheck{}
	}
	server.WriteJSON(w, http.StatusOK, checks)
}

func (h *handler) results(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	results, err := h.store.LatestResults(r.Context(), id, limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get health check results")
		return
	}
	if results == nil {
		results = []store.HealthCheckResult{}
	}
	server.WriteJSON(w, http.StatusOK, results)
}

func (h *handler) uptimeSummary(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if hStr := r.URL.Query().Get("hours"); hStr != "" {
		if v, err := strconv.Atoi(hStr); err == nil && v > 0 {
			hours = v
		}
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	summaries, err := h.store.UptimeSummaries(r.Context(), since)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get uptime summaries")
		return
	}
	if summaries == nil {
		summaries = []store.UptimeSummary{}
	}

	server.WriteJSON(w, http.StatusOK, summaries)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var params store.CreateHealthCheckParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if params.Name == "" || params.URL == "" {
		server.WriteError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	if len(params.URL) > 2048 {
		server.WriteError(w, http.StatusBadRequest, "URL too long (max 2048 characters)")
		return
	}
	u, parseErr := url.Parse(params.URL)
	if parseErr != nil || u.Host == "" {
		server.WriteError(w, http.StatusBadRequest, "invalid URL format")
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		server.WriteError(w, http.StatusBadRequest, "only http and https URL schemes are allowed")
		return
	}
	if params.ExpectedStatus != 0 && (params.ExpectedStatus < 100 || params.ExpectedStatus > 599) {
		server.WriteError(w, http.StatusBadRequest, "expected_status must be between 100 and 599")
		return
	}

	hc, err := h.store.Create(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create health check")
		return
	}
	server.WriteJSON(w, http.StatusCreated, hc)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.Delete(r.Context(), id); err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "health check not found")
		return
	} else if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to delete health check")
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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

func (h *handler) healthPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Health", "health")
	var uptimeSummaries []store.UptimeSummary
	var healthChecks []store.HealthCheck
	if h.store != nil {
		checks, err := h.store.List(r.Context(), store.ListHealthCheckParams{})
		if err == nil {
			healthChecks = checks
		}
		oneDayAgo := time.Now().Add(-24 * time.Hour)
		summaries, err := h.store.UptimeSummaries(r.Context(), oneDayAgo)
		if err == nil {
			uptimeSummaries = summaries
		}
	}
	webviews.HealthPage(layout, uptimeSummaries, healthChecks).Render(r.Context(), w)
}
