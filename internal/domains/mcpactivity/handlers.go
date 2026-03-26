package mcpactivity

import (
	"net/http"
	"strconv"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type handler struct {
	store store.MCPActivityStore
}

func (h *handler) stats(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"active_sessions": 0, "calls_last_hour": 0, "errors_last_hour": 0,
		})
		return
	}

	stats, err := h.store.Stats(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get MCP activity stats")
		return
	}

	server.WriteJSON(w, http.StatusOK, stats)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		server.WriteJSON(w, http.StatusOK, []any{})
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	events, err := h.store.Recent(r.Context(), limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get MCP activity")
		return
	}

	server.WriteJSON(w, http.StatusOK, events)
}
