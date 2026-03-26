package investigations

import (
	"errors"
	"net/http"
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
	sessionStore     store.InvestigationSessionStore
	mcpActivityStore store.MCPActivityStore
	cfg              *config.Config
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if h.sessionStore == nil {
		server.WriteError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	params := store.ListInvestigationSessionParams{}

	if v := r.URL.Query().Get("status"); v != "" {
		params.Status = store.InvestigationSessionStatus(v)
	}
	if v := r.URL.Query().Get("intent"); v != "" {
		params.Intent = v
	}
	if v := r.URL.Query().Get("service"); v != "" {
		params.Service = v
	}
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			params.Since = t
		}
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

	sessions, err := h.sessionStore.List(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list investigation sessions")
		return
	}

	server.WriteJSON(w, http.StatusOK, sessions)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	if h.sessionStore == nil {
		server.WriteError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	id := chi.URLParam(r, "id")
	sess, err := h.sessionStore.GetByID(r.Context(), id)
	if err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "investigation session not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get investigation session")
		return
	}

	server.WriteJSON(w, http.StatusOK, sess)
}

func (h *handler) stats(w http.ResponseWriter, r *http.Request) {
	if h.sessionStore == nil {
		server.WriteError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	stats, err := h.sessionStore.Stats(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get investigation stats")
		return
	}

	server.WriteJSON(w, http.StatusOK, stats)
}

func (h *handler) steps(w http.ResponseWriter, r *http.Request) {
	if h.sessionStore == nil || h.mcpActivityStore == nil {
		server.WriteError(w, http.StatusNotFound, "investigation sessions not available")
		return
	}

	id := chi.URLParam(r, "id")

	// Verify session exists
	_, err := h.sessionStore.GetByID(r.Context(), id)
	if err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "investigation session not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get investigation session")
		return
	}

	events, err := h.mcpActivityStore.ListByInvestigationSession(r.Context(), id)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list investigation steps")
		return
	}

	server.WriteJSON(w, http.StatusOK, events)
}

// ── Page handlers ────────────────────────────────────────────

func (h *handler) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := server.UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:     title,
		Nav:       nav,
		User:      user,
		IsAdmin:   isAdmin,
		CSRFToken: server.CSRFToken(r.Context()),
		DevMode:   h.cfg != nil && h.cfg.DevMode,
	}
}

func (h *handler) sessionsPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Sessions", "sessions")

	page := webviews.SessionsPageData{
		Status: r.URL.Query().Get("status"),
	}

	if h.sessionStore != nil {
		statusFilter := store.InvestigationSessionStatus(r.URL.Query().Get("status"))

		sessions, err := h.sessionStore.List(r.Context(), store.ListInvestigationSessionParams{
			Status: statusFilter,
			Limit:  100,
		})
		if err == nil {
			page.Sessions = sessions
		}

		stats, err := h.sessionStore.Stats(r.Context())
		if err == nil {
			page.Stats = stats
		}
	}

	webviews.SessionsPage(layout, page).Render(r.Context(), w)
}

func (h *handler) sessionDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		server.WriteError(w, http.StatusBadRequest, "missing session id")
		return
	}

	if h.sessionStore == nil {
		server.WriteError(w, http.StatusNotFound, "sessions not available")
		return
	}

	sess, err := h.sessionStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		server.WriteError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get session")
		return
	}

	var activities []store.MCPActivityEvent
	if h.mcpActivityStore != nil {
		activities, _ = h.mcpActivityStore.ListByInvestigationSession(r.Context(), id)
	}

	layout := h.layoutData(r, "Session Detail", "sessions")
	detail := webviews.SessionDetailData{
		Session: sess,
		Events:  activities,
	}

	webviews.SessionDetailPage(layout, detail).Render(r.Context(), w)
}
