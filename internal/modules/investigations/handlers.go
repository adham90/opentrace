package investigations

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

type handler struct {
	sessionStore     store.InvestigationSessionStore
	mcpActivityStore store.MCPActivityStore
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
