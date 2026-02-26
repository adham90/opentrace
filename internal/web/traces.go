package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

// handleGetTraceStatus returns the status of a single trace.
func (s *Server) handleGetTraceStatus(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "traceID")
	if traceID == "" {
		writeError(w, http.StatusBadRequest, "traceID is required")
		return
	}

	ts, err := s.traceStore.GetTraceStatus(r.Context(), traceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "trace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get trace status")
		return
	}

	writeJSON(w, http.StatusOK, ts)
}

// handleListRecentTraces returns a paginated list of recent traces with status.
func (s *Server) handleListRecentTraces(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	traces, total, err := s.traceStore.ListRecentTraces(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list traces")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"traces": traces,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}
