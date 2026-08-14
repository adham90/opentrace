package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

func (h *handler) handleLogDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.deps.LogStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "log store not available")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		// Non-numeric segments land here because /logs/{id} is the catch-all
		// under /logs. Name the paths that do exist: a caller who guessed a
		// route gets a map instead of a puzzle about log ids they never used.
		server.WriteError(w, http.StatusNotFound, fmt.Sprintf(
			"no such logs endpoint %q — use /logs/tail (supports search, level, service, env, since, after, limit), /logs/search, /logs/stream, or /logs/{numeric-id}",
			idStr,
		))
		return
	}

	entry, err := h.deps.LogStore.GetByID(ctx, id)
	if err != nil {
		// Only the sentinel means "no such log". A read failure reported as 404
		// tells the operator the log was pruned and leaves no trace of the
		// actual fault.
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "log not found")
			return
		}
		slog.Error("cli log detail: lookup failed", "id", id, "error", err)
		server.WriteError(w, http.StatusInternalServerError, "log lookup failed")
		return
	}

	server.WriteJSON(w, http.StatusOK, entry)
}

// logTailResponse is the JSON response for GET /api/cli/logs/tail.
type logTailResponse struct {
	Logs    []store.LogEntry `json:"logs"`
	Cursor  int64            `json:"cursor"`
	HasMore bool             `json:"has_more"`
}

func (h *handler) handleLogsTail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.deps.LogStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "log store not available")
		return
	}

	params := store.LogSearchParams{
		Limit: 50,
	}

	hasCursor := false
	if v := r.URL.Query().Get("after"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			server.WriteError(w, http.StatusBadRequest, "invalid 'after' cursor")
			return
		}
		params.SinceID = id
		params.SortAsc = true // polling: ascending from cursor
		hasCursor = true
	}

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			params.Limit = n
		}
	}

	if v := r.URL.Query().Get("level"); v != "" {
		params.Level = v
	}
	if v := r.URL.Query().Get("service"); v != "" {
		params.Service = v
	}
	if v := r.URL.Query().Get("env"); v != "" {
		params.Environment = v
	}
	if v := searchTerm(r); v != "" {
		params.Query = v
	}
	// A bad 'since' is rejected rather than ignored. Silently dropping it
	// returns the newest logs regardless of the window, which reads as "nothing
	// happened back then" — the worst possible answer to a question about a
	// past incident.
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := server.ParseSinceParam(v)
		if err != nil {
			server.WriteError(w, http.StatusBadRequest, "invalid 'since' value (use 15m, 6h, 7d or an RFC3339 timestamp)")
			return
		}
		params.Start = &t
	}

	entries, err := h.deps.LogStore.Search(ctx, params)
	if err != nil {
		slog.Error("cli logs tail: search failed", "error", err)
		server.WriteError(w, http.StatusInternalServerError, "log search failed")
		return
	}

	// Initial fetch (no cursor): results are newest-first from DB.
	// Reverse to ascending order so cursor tracking works consistently.
	if !hasCursor && len(entries) > 1 {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}

	resp := logTailResponse{
		Logs:    entries,
		HasMore: len(entries) == params.Limit,
	}

	if len(entries) > 0 {
		resp.Cursor = entries[len(entries)-1].ID
	}

	server.WriteJSON(w, http.StatusOK, resp)
}

// handleLogsStream implements GET /api/cli/logs/stream as Server-Sent Events.
// It polls LogStore.Search every 2 seconds and streams new entries to the client.
func (h *handler) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.deps.LogStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "log store not available")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		server.WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Clear the global http.Server WriteTimeout for this long-lived SSE stream;
	// otherwise `opentrace logs` disconnects after exactly WriteTimeout (60s).
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("cli logs stream: clearing write deadline failed", "error", err)
	}

	level := r.URL.Query().Get("level")
	service := r.URL.Query().Get("service")
	env := r.URL.Query().Get("env")
	search := searchTerm(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Start from the latest log entry
	var cursor int64
	latest, err := h.deps.LogStore.Search(ctx, store.LogSearchParams{
		Limit:       1,
		Level:       level,
		Service:     service,
		Environment: env,
		Query:       search,
	})
	if err == nil && len(latest) > 0 {
		cursor = latest[0].ID
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := h.deps.LogStore.Search(ctx, store.LogSearchParams{
				SinceID:     cursor,
				Limit:       100,
				SortAsc:     true,
				Level:       level,
				Service:     service,
				Environment: env,
				Query:       search,
			})
			if err != nil {
				slog.Error("cli logs stream: poll failed", "error", err)
				continue
			}

			for _, entry := range entries {
				data, err := json.Marshal(entry)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
				cursor = entry.ID
			}

			if len(entries) > 0 {
				flusher.Flush()
			}
		}
	}
}

// searchTerm reads the free-text filter, accepting "q" as an alias for
// "search". /logs/search is served by the tail handler, and a caller reaching
// for the conventional ?q= otherwise got every log back, unfiltered, presented
// as search results — a wrong answer stated confidently, which is worse than
// an error about an unknown parameter.
func searchTerm(r *http.Request) string {
	if v := r.URL.Query().Get("search"); v != "" {
		return v
	}
	return r.URL.Query().Get("q")
}
