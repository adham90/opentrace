package traces

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

type handler struct {
	store store.TraceStore
}

func (h *handler) getStatus(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "traceID")
	if traceID == "" {
		server.WriteError(w, http.StatusBadRequest, "traceID is required")
		return
	}

	ts, err := h.store.GetTraceStatus(r.Context(), traceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "trace not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to get trace status")
		return
	}

	server.WriteJSON(w, http.StatusOK, ts)
}

func (h *handler) listRecent(w http.ResponseWriter, r *http.Request) {
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

	traces, total, err := h.store.ListRecentTraces(r.Context(), limit, offset)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list traces")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"traces": traces,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}
