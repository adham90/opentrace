package web

import (
	"net/http"
	"strconv"
)

// handleMCPActivityStats returns aggregated MCP activity statistics.
func (s *Server) handleMCPActivityStats(w http.ResponseWriter, r *http.Request) {
	if s.mcpActivityStore == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"active_sessions": 0, "calls_last_hour": 0, "errors_last_hour": 0,
		})
		return
	}

	stats, err := s.mcpActivityStore.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get MCP activity stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// handleMCPActivity returns recent MCP activity events.
func (s *Server) handleMCPActivity(w http.ResponseWriter, r *http.Request) {
	if s.mcpActivityStore == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	events, err := s.mcpActivityStore.Recent(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get MCP activity")
		return
	}

	writeJSON(w, http.StatusOK, events)
}
