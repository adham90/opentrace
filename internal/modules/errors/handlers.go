package errors

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

type handler struct {
	store store.ErrorGroupStore
	db    *sql.DB
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
