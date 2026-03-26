package logs

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/sqlite"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

type handler struct {
	logStore store.LogStore
	db       *sql.DB
	cfg      *config.Config
}

// LogFilters holds the current filter state for the logs page.
type LogFilters struct {
	Query            string
	Service          string
	Level            string
	EventType        string
	TimeRange        string
	Environment      string
	CommitHash       string
	RequestID        string
	TraceID          string
	ExceptionClass   string
	ErrorFingerprint string
	SourceFile       string
}

// parseTimeRange converts a time range preset string to a Start time pointer.
func parseTimeRange(preset string) *time.Time {
	var d time.Duration
	switch preset {
	case "15m":
		d = 15 * time.Minute
	case "1h":
		d = time.Hour
	case "6h":
		d = 6 * time.Hour
	case "24h":
		d = 24 * time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	default:
		return nil
	}
	t := time.Now().Add(-d)
	return &t
}

// parseMetadataParams extracts "meta.key=value" query params into a map.
func parseMetadataParams(q url.Values) map[string]string {
	m := make(map[string]string)
	for key, vals := range q {
		if strings.HasPrefix(key, "meta.") && len(vals) > 0 && vals[0] != "" {
			m[strings.TrimPrefix(key, "meta.")] = vals[0]
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// parseExcludeParams extracts "exclude_field=value" query params into a map.
func parseExcludeParams(q url.Values) map[string]string {
	m := make(map[string]string)
	allowed := []string{"service", "level", "environment", "event_type",
		"exception_class", "error_fingerprint", "source_file", "commit_hash"}
	for _, f := range allowed {
		if v := q.Get("exclude_" + f); v != "" {
			m[f] = v
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func parseFilters(r *http.Request) LogFilters {
	return LogFilters{
		Query:            r.URL.Query().Get("query"),
		Service:          r.URL.Query().Get("service"),
		Level:            r.URL.Query().Get("level"),
		EventType:        r.URL.Query().Get("event_type"),
		TimeRange:        r.URL.Query().Get("time_range"),
		Environment:      r.URL.Query().Get("environment"),
		CommitHash:       r.URL.Query().Get("commit_hash"),
		RequestID:        r.URL.Query().Get("request_id"),
		TraceID:          r.URL.Query().Get("trace_id"),
		ExceptionClass:   r.URL.Query().Get("exception_class"),
		ErrorFingerprint: r.URL.Query().Get("error_fingerprint"),
		SourceFile:       r.URL.Query().Get("source_file"),
	}
}

func buildSearchParams(filters LogFilters, r *http.Request, limit, offset int) store.LogSearchParams {
	params := store.LogSearchParams{
		Query:            filters.Query,
		Service:          filters.Service,
		Level:            filters.Level,
		EventType:        filters.EventType,
		Start:            parseTimeRange(filters.TimeRange),
		Limit:            limit,
		Offset:           offset,
		MetadataFilter:   parseMetadataParams(r.URL.Query()),
		Exclude:          parseExcludeParams(r.URL.Query()),
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
		TraceID:          filters.TraceID,
		ExceptionClass:   filters.ExceptionClass,
		ErrorFingerprint: filters.ErrorFingerprint,
		SourceFile:       filters.SourceFile,
	}
	if startStr := r.URL.Query().Get("start"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			params.Start = &t
		}
	}
	if endStr := r.URL.Query().Get("end"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			params.End = &t
		}
	}
	return params
}

func parseLimitOffset(r *http.Request) (int, int) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	return limit, offset
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

func (h *handler) logsPage(w http.ResponseWriter, r *http.Request) {
	filters := parseFilters(r)
	limit, offset := parseLimitOffset(r)

	searchParams := buildSearchParams(filters, r, limit+1, offset)

	var logEntries []store.LogEntry
	var hasMore bool
	var maxID int64
	if h.logStore != nil {
		var err error
		logEntries, err = h.logStore.Search(r.Context(), searchParams)
		if err != nil {
			logEntries = nil
		}
		hasMore = len(logEntries) > limit
		if hasMore {
			logEntries = logEntries[:limit]
		}
		for _, l := range logEntries {
			if l.ID > maxID {
				maxID = l.ID
			}
		}
	}

	layout := h.layoutData(r, "Logs", "logs")
	logsData := webviews.LogsPageData{
		Filters: webviews.LogFilters{
			Query:            filters.Query,
			Service:          filters.Service,
			Level:            filters.Level,
			EventType:        filters.EventType,
			TimeRange:        filters.TimeRange,
			Environment:      filters.Environment,
			CommitHash:       filters.CommitHash,
			RequestID:        filters.RequestID,
			TraceID:          filters.TraceID,
			ExceptionClass:   filters.ExceptionClass,
			ErrorFingerprint: filters.ErrorFingerprint,
			SourceFile:       filters.SourceFile,
		},
		Logs:     logEntries,
		MaxLogID: maxID,
		HasMore:  hasMore,
	}
	webviews.LogsPage(layout, logsData).Render(r.Context(), w)
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func (h *handler) logsFragment(w http.ResponseWriter, r *http.Request) {
	if !isHTMX(r) {
		http.Redirect(w, r, "/logs", http.StatusMovedPermanently)
		return
	}

	filters := parseFilters(r)
	limit, offset := parseLimitOffset(r)

	searchParams := buildSearchParams(filters, r, limit+1, offset)

	logs, err := h.logStore.Search(r.Context(), searchParams)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to search logs")
		return
	}

	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}

	var maxID int64
	for _, l := range logs {
		if l.ID > maxID {
			maxID = l.ID
		}
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"logs":     logs,
		"offset":   offset,
		"limit":    limit,
		"has_more": hasMore,
		"max_id":   maxID,
	})
}

// ── API handlers ─────────────────────────────────────────────

func (h *handler) logsPoll(w http.ResponseWriter, r *http.Request) {
	sinceID := int64(0)
	if v := r.URL.Query().Get("since_id"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			sinceID = parsed
		}
	}

	if sinceID == 0 {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		return
	}

	filters := parseFilters(r)

	pollParams := store.LogSearchParams{
		Query:            filters.Query,
		Service:          filters.Service,
		Level:            filters.Level,
		EventType:        filters.EventType,
		Start:            parseTimeRange(filters.TimeRange),
		SinceID:          sinceID,
		Limit:            200,
		MetadataFilter:   parseMetadataParams(r.URL.Query()),
		Exclude:          parseExcludeParams(r.URL.Query()),
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
		TraceID:          filters.TraceID,
		ExceptionClass:   filters.ExceptionClass,
		ErrorFingerprint: filters.ErrorFingerprint,
		SourceFile:       filters.SourceFile,
	}
	if s := r.URL.Query().Get("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			pollParams.Start = &t
		}
	}
	if e := r.URL.Query().Get("end"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			pollParams.End = &t
		}
	}
	logEntries, err := h.logStore.Search(r.Context(), pollParams)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if len(logEntries) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	var maxID int64
	for _, l := range logEntries {
		if l.ID > maxID {
			maxID = l.ID
		}
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"logs":   logEntries,
		"max_id": maxID,
	})
}

func (h *handler) logsHistogram(w http.ResponseWriter, r *http.Request) {
	if h.logStore == nil {
		server.WriteJSON(w, http.StatusOK, []store.LogHistogramBucket{})
		return
	}

	timeRange := r.URL.Query().Get("time_range")
	if timeRange == "" {
		timeRange = "1h"
	}

	var duration time.Duration
	switch timeRange {
	case "15m":
		duration = 15 * time.Minute
	case "1h":
		duration = time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	default:
		duration = time.Hour
	}

	var interval time.Duration
	switch timeRange {
	case "15m":
		interval = time.Minute
	case "1h":
		interval = 5 * time.Minute
	case "6h":
		interval = 15 * time.Minute
	case "24h":
		interval = time.Hour
	case "7d":
		interval = 6 * time.Hour
	default:
		interval = 5 * time.Minute
	}

	now := time.Now()
	params := store.LogHistogramParams{
		Since:    now.Add(-duration),
		Until:    now,
		Interval: interval,
		Service:  r.URL.Query().Get("service"),
		Level:    r.URL.Query().Get("level"),
	}

	buckets, err := h.logStore.Histogram(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to compute histogram")
		return
	}
	if buckets == nil {
		buckets = []store.LogHistogramBucket{}
	}
	server.WriteJSON(w, http.StatusOK, buckets)
}

func (h *handler) getLogDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid log id")
		return
	}
	entry, err := h.logStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		server.WriteError(w, http.StatusNotFound, "log not found")
		return
	} else if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get log")
		return
	}
	server.WriteJSON(w, http.StatusOK, entry)
}

func (h *handler) listServices(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		server.WriteJSON(w, http.StatusOK, []string{})
		return
	}
	services, err := sqlite.ListServices(r.Context(), h.db)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list services")
		return
	}
	if services == nil {
		services = []string{}
	}
	server.WriteJSON(w, http.StatusOK, services)
}

func (h *handler) listEventTypes(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		server.WriteJSON(w, http.StatusOK, []string{})
		return
	}
	types, err := sqlite.ListEventTypes(r.Context(), h.db)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list event types")
		return
	}
	if types == nil {
		types = []string{}
	}
	server.WriteJSON(w, http.StatusOK, types)
}

func (h *handler) logValues(w http.ResponseWriter, r *http.Request) {
	if h.logStore == nil {
		server.WriteJSON(w, http.StatusOK, []string{})
		return
	}
	field := r.URL.Query().Get("field")
	if field == "" {
		server.WriteError(w, http.StatusBadRequest, "field parameter required")
		return
	}
	now := time.Now().UTC()
	params := store.LogCountParams{
		Since: now.Add(-7 * 24 * time.Hour),
		Until: now,
	}
	if field == "metadata_key" {
		keys, err := h.logStore.MetadataKeys(r.Context(), params)
		if err != nil {
			server.WriteJSON(w, http.StatusOK, []string{})
			return
		}
		if keys == nil {
			keys = []string{}
		}
		server.WriteJSON(w, http.StatusOK, keys)
		return
	}
	values, err := h.logStore.DistinctValues(r.Context(), field, params)
	if err != nil {
		server.WriteJSON(w, http.StatusOK, []string{})
		return
	}
	if values == nil {
		values = []string{}
	}
	server.WriteJSON(w, http.StatusOK, values)
}
