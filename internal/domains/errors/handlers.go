package errors

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/config"
	errorviews "github.com/adham90/opentrace/internal/domains/errors/views"
	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/views"
)

type handler struct {
	store    store.ErrorGroupStore
	logStore store.LogStore
	db       *sql.DB
	cfg      *config.Config
}

func (h *handler) batch(w http.ResponseWriter, r *http.Request) {
	fps := r.URL.Query().Get("fingerprints")
	if fps == "" {
		server.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}

	fingerprints := strings.Split(fps, ",")
	if len(fingerprints) > 50 {
		fingerprints = fingerprints[:50]
	}

	type batchEntry struct {
		OccurrenceCount int    `json:"occurrence_count"`
		Status          string `json:"status"`
		ExceptionClass  string `json:"exception_class"`
	}

	result := make(map[string]batchEntry)
	for _, fp := range fingerprints {
		fp = strings.TrimSpace(fp)
		if fp == "" {
			continue
		}
		eg, err := h.store.Get(r.Context(), fp)
		if err != nil {
			continue
		}
		result[fp] = batchEntry{
			OccurrenceCount: eg.OccurrenceCount,
			Status:          string(eg.Status),
			ExceptionClass:  eg.ExceptionClass,
		}
	}

	server.WriteJSON(w, http.StatusOK, result)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	params := store.ListErrorGroupParams{
		Status:      store.ErrorGroupStatus(r.URL.Query().Get("status")),
		Service:     r.URL.Query().Get("service"),
		Environment: r.URL.Query().Get("environment"),
		SortBy:      r.URL.Query().Get("sort_by"),
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			params.Limit = v
		}
	}

	groups, err := h.store.List(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list error groups")
		return
	}
	if groups == nil {
		groups = []store.ErrorGroup{}
	}
	server.WriteJSON(w, http.StatusOK, groups)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")
	eg, err := h.store.Get(r.Context(), fp)
	if err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "error group not found")
		return
	} else if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get error group")
		return
	}

	events, _ := h.store.ListEvents(r.Context(), fp, 10)
	eg.Events = events

	server.WriteJSON(w, http.StatusOK, eg)
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := h.store.Resolve(r.Context(), fp, body.Reason); err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "error group not found")
		return
	} else if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to resolve error group")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func (h *handler) ignore(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := h.store.Ignore(r.Context(), fp, body.Reason); err == store.ErrNotFound {
		server.WriteError(w, http.StatusNotFound, "error group not found")
		return
	} else if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to ignore error group")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
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

func (h *handler) errorsPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Errors", "errors")
	var errorGroups []store.ErrorGroup
	if h.store != nil {
		status := store.ErrorGroupStatus(r.URL.Query().Get("status"))
		groups, err := h.store.List(r.Context(), store.ListErrorGroupParams{
			Status:  status,
			Service: r.URL.Query().Get("service"),
			SortBy:  "last_seen_at",
			Limit:   100,
		})
		if err == nil {
			errorGroups = groups
		}
	}
	errorviews.ErrorsPage(layout, errorGroups).Render(r.Context(), w)
}

func (h *handler) errorDetailPage(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")
	if fp == "" {
		http.Redirect(w, r, "/errors", http.StatusFound)
		return
	}
	if h.store == nil {
		server.WriteError(w, http.StatusNotFound, "error tracking not available")
		return
	}

	eg, err := h.store.Get(r.Context(), fp)
	if errors.Is(err, store.ErrNotFound) {
		server.WriteError(w, http.StatusNotFound, "error group not found")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get error group")
		return
	}

	events, _ := h.store.ListEvents(r.Context(), fp, 20)
	eg.Events = events

	layout := h.layoutData(r, eg.ExceptionClass, "errors")

	// Fetch recent log entries for this error fingerprint
	var errorLogs []store.LogEntry
	if h.logStore != nil {
		logs, err := h.logStore.Search(r.Context(), store.LogSearchParams{
			ErrorFingerprint: fp,
			Limit:            25,
		})
		if err == nil {
			errorLogs = logs
		}
	}

	errorviews.ErrorDetailPage(layout, *eg, errorLogs).Render(r.Context(), w)
}

func (h *handler) histogram(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")
	if fp == "" {
		server.WriteError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}
	if h.db == nil {
		server.WriteJSON(w, http.StatusOK, []map[string]any{})
		return
	}

	timeRange := r.URL.Query().Get("time_range")
	if timeRange == "" {
		timeRange = "7d"
	}

	var duration, interval time.Duration
	switch timeRange {
	case "15m":
		duration, interval = 15*time.Minute, time.Minute
	case "1h":
		duration, interval = time.Hour, 5*time.Minute
	case "6h":
		duration, interval = 6*time.Hour, 15*time.Minute
	case "24h":
		duration, interval = 24*time.Hour, time.Hour
	case "7d":
		duration, interval = 7*24*time.Hour, 6*time.Hour
	default:
		duration, interval = 7*24*time.Hour, 6*time.Hour
	}

	now := time.Now()
	since := now.Add(-duration)
	type bucket struct {
		Timestamp time.Time `json:"timestamp"`
		Count     int       `json:"count"`
	}
	var buckets []bucket
	for t := since; t.Before(now); t = t.Add(interval) {
		end := t.Add(interval)
		if end.After(now) {
			end = now
		}
		var count int
		err := h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM logs WHERE error_fingerprint = ? AND timestamp >= ? AND timestamp < ?`,
			fp, t.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano),
		).Scan(&count)
		if err != nil {
			server.WriteError(w, http.StatusInternalServerError, "histogram query failed")
			return
		}
		buckets = append(buckets, bucket{Timestamp: t, Count: count})
	}
	if buckets == nil {
		buckets = []bucket{}
	}
	server.WriteJSON(w, http.StatusOK, buckets)
}
