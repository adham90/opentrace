package web

import (
	"embed"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	errorviews "github.com/adham90/opentrace/internal/modules/errors/views"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

//go:embed static/*
var staticFS embed.FS

type LogFilters struct {
	Query            string
	Service          string
	Level            string
	EventType        string
	TimeRange        string // preset: 15m, 1h, 6h, 24h, 7d, or empty (all)
	Environment      string
	CommitHash       string
	RequestID        string
	TraceID          string
	ExceptionClass   string
	ErrorFingerprint string
	SourceFile       string
}

// parseTimeRange converts a time range preset string to a Start time pointer.
// Returns nil if the preset is empty or unrecognized (meaning "all time").
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

func (s *Server) isDevMode() bool {
	return s.cfg != nil && s.cfg.DevMode
}

// layoutData creates a views.LayoutData for templ rendering.
func (s *Server) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
		DevMode: s.isDevMode(),
	}
}


func (s *Server) handleLogsPage(w http.ResponseWriter, r *http.Request) {
	filters := LogFilters{
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

	searchParams := store.LogSearchParams{
		Query:            filters.Query,
		Service:          filters.Service,
		Level:            filters.Level,
		EventType:        filters.EventType,
		Start:            parseTimeRange(filters.TimeRange),
		Limit:            limit + 1,
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
			searchParams.Start = &t
		}
	}
	if endStr := r.URL.Query().Get("end"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			searchParams.End = &t
		}
	}

	var logEntries []store.LogEntry
	var hasMore bool
	var maxID int64
	if s.logStore != nil {
		var err error
		logEntries, err = s.logStore.Search(r.Context(), searchParams)
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

	layout := s.layoutData(r, "Logs", "logs")
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

func (s *Server) handleErrorsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Errors", "errors")
	var errorGroups []store.ErrorGroup
	if s.errorGroupStore != nil {
		status := store.ErrorGroupStatus(r.URL.Query().Get("status"))
		groups, err := s.errorGroupStore.List(r.Context(), store.ListErrorGroupParams{
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

func (s *Server) handleErrorDetailPage(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")
	if fp == "" {
		http.Redirect(w, r, "/errors", http.StatusFound)
		return
	}
	if s.errorGroupStore == nil {
		writeError(w, http.StatusNotFound, "error tracking not available")
		return
	}

	eg, err := s.errorGroupStore.Get(r.Context(), fp)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "error group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get error group")
		return
	}

	events, _ := s.errorGroupStore.ListEvents(r.Context(), fp, 20)
	eg.Events = events

	layout := s.layoutData(r, eg.ExceptionClass, "errors")

	// Fetch recent log entries for this error fingerprint
	var errorLogs []store.LogEntry
	if s.logStore != nil {
		logs, err := s.logStore.Search(r.Context(), store.LogSearchParams{
			ErrorFingerprint: fp,
			Limit:            25,
		})
		if err == nil {
			errorLogs = logs
		}
	}

	errorviews.ErrorDetailPage(layout, *eg, errorLogs).Render(r.Context(), w)
}

// Watcher page handlers moved to internal/modules/watches/.

func (s *Server) handleHealthPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Health", "health")
	var uptimeSummaries []store.UptimeSummary
	var healthChecks []store.HealthCheck
	if s.healthCheckStore != nil {
		checks, err := s.healthCheckStore.List(r.Context(), store.ListHealthCheckParams{})
		if err == nil {
			healthChecks = checks
		}
		oneDayAgo := time.Now().Add(-24 * time.Hour)
		summaries, err := s.healthCheckStore.UptimeSummaries(r.Context(), oneDayAgo)
		if err == nil {
			uptimeSummaries = summaries
		}
	}
	webviews.HealthPage(layout, uptimeSummaries, healthChecks).Render(r.Context(), w)
}

// handleLogsFragment serves HTMX log fragments for the logs page.
func (s *Server) handleLogsFragment(w http.ResponseWriter, r *http.Request) {
	if !isHTMX(r) {
		http.Redirect(w, r, "/logs", http.StatusMovedPermanently)
		return
	}

	filters := LogFilters{
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

	searchParams := store.LogSearchParams{
		Query:            filters.Query,
		Service:          filters.Service,
		Level:            filters.Level,
		EventType:        filters.EventType,
		Start:            parseTimeRange(filters.TimeRange),
		Limit:            limit + 1,
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
	if s := r.URL.Query().Get("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			searchParams.Start = &t
		}
	}
	if e := r.URL.Query().Get("end"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			searchParams.End = &t
		}
	}

	logs, err := s.logStore.Search(r.Context(), searchParams)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search logs")
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

	writeJSON(w, http.StatusOK, map[string]any{
		"logs":     logs,
		"offset":   offset,
		"limit":    limit,
		"has_more": hasMore,
		"max_id":   maxID,
	})
}

func (s *Server) handleLogsPoll(w http.ResponseWriter, r *http.Request) {
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

	filters := LogFilters{
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
	logs, err := s.logStore.Search(r.Context(), pollParams)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if len(logs) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	var maxID int64
	for _, l := range logs {
		if l.ID > maxID {
			maxID = l.ID
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"logs":   logs,
		"max_id": maxID,
	})
}

func (s *Server) handleLogsHistogram(w http.ResponseWriter, r *http.Request) {
	if s.logStore == nil {
		writeJSON(w, http.StatusOK, []store.LogHistogramBucket{})
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

	// Auto interval based on time range
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

	buckets, err := s.logStore.Histogram(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute histogram")
		return
	}
	if buckets == nil {
		buckets = []store.LogHistogramBucket{}
	}
	writeJSON(w, http.StatusOK, buckets)
}

func (s *Server) handleGetLogDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid log id")
		return
	}
	entry, err := s.logStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "log not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get log")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}


func (s *Server) handleToolsAPI(w http.ResponseWriter, r *http.Request) {
	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Access      string `json:"access"`
		Requires    string `json:"requires,omitempty"`
	}

	type toolCategory struct {
		Name        string     `json:"name"`
		Description string     `json:"description"`
		Tools       []toolInfo `json:"tools"`
	}

	var categories []toolCategory

	// Use the auto-detected catalog when available.
	if s.toolCatalog != nil {
		for _, cat := range s.toolCatalog.Categories() {
			tc := toolCategory{Name: cat.Name, Description: cat.Description}
			for _, t := range cat.Tools {
				tc.Tools = append(tc.Tools, toolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      t.Access,
					Requires:    t.Requires,
				})
			}
			categories = append(categories, tc)
		}
	}

	// Append dynamic connector tools from the registry (added after startup).
	if s.registry != nil {
		dynamicTools := s.registry.AllTools()
		if len(dynamicTools) > 0 {
			dynCat := toolCategory{
				Name:        "Connector Queries",
				Description: "Dynamic tools registered by active database connectors",
			}
			for _, t := range dynamicTools {
				dynCat.Tools = append(dynCat.Tools, toolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      "admin",
					Requires:    "database connector",
				})
			}
			categories = append(categories, dynCat)
		}
	}

	writeJSON(w, http.StatusOK, categories)
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	services, err := store.ListServices(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list services")
		return
	}
	if services == nil {
		services = []string{}
	}
	writeJSON(w, http.StatusOK, services)
}

func (s *Server) handleListEventTypes(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	types, err := store.ListEventTypes(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list event types")
		return
	}
	if types == nil {
		types = []string{}
	}
	writeJSON(w, http.StatusOK, types)
}

func (s *Server) handleLogValues(w http.ResponseWriter, r *http.Request) {
	if s.logStore == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	field := r.URL.Query().Get("field")
	if field == "" {
		writeError(w, http.StatusBadRequest, "field parameter required")
		return
	}
	now := time.Now().UTC()
	params := store.LogCountParams{
		Since: now.Add(-7 * 24 * time.Hour),
		Until: now,
	}
	if field == "metadata_key" {
		keys, err := s.logStore.MetadataKeys(r.Context(), params)
		if err != nil {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		if keys == nil {
			keys = []string{}
		}
		writeJSON(w, http.StatusOK, keys)
		return
	}
	values, err := s.logStore.DistinctValues(r.Context(), field, params)
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	if values == nil {
		values = []string{}
	}
	writeJSON(w, http.StatusOK, values)
}

// Settings, setup, and users page handlers moved to internal/modules/settings/.

func (s *Server) handleConnectorsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Connectors", "connectors")
	var dataSources []store.DataSource
	if s.dsStore != nil {
		ds, err := s.dsStore.List(r.Context(), store.ListDataSourceParams{})
		if err == nil {
			dataSources = ds
		}
	}
	webviews.ConnectorsPage(layout, dataSources).Render(r.Context(), w)
}

func (s *Server) handleToolsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Tools", "tools")

	var categories []webviews.ToolCategory
	if s.toolCatalog != nil {
		for _, cat := range s.toolCatalog.Categories() {
			tc := webviews.ToolCategory{Name: cat.Name, Description: cat.Description}
			for _, t := range cat.Tools {
				tc.Tools = append(tc.Tools, webviews.ToolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      t.Access,
					Requires:    t.Requires,
				})
			}
			categories = append(categories, tc)
		}
	}
	if s.registry != nil {
		dynamicTools := s.registry.AllTools()
		if len(dynamicTools) > 0 {
			dynCat := webviews.ToolCategory{
				Name:        "Connector Queries",
				Description: "Dynamic tools registered by active database connectors",
			}
			for _, t := range dynamicTools {
				dynCat.Tools = append(dynCat.Tools, webviews.ToolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      "admin",
					Requires:    "database connector",
				})
			}
			categories = append(categories, dynCat)
		}
	}

	webviews.ToolsPage(layout, categories).Render(r.Context(), w)
}

func (s *Server) handleSessionsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Sessions", "sessions")

	page := webviews.SessionsPageData{
		Status: r.URL.Query().Get("status"),
	}

	if s.investigationSessionStore != nil {
		statusFilter := store.InvestigationSessionStatus(r.URL.Query().Get("status"))

		sessions, err := s.investigationSessionStore.List(r.Context(), store.ListInvestigationSessionParams{
			Status: statusFilter,
			Limit:  100,
		})
		if err == nil {
			page.Sessions = sessions
		}

		stats, err := s.investigationSessionStore.Stats(r.Context())
		if err == nil {
			page.Stats = stats
		}
	}

	webviews.SessionsPage(layout, page).Render(r.Context(), w)
}

func (s *Server) handleSessionDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	if s.investigationSessionStore == nil {
		writeError(w, http.StatusNotFound, "sessions not available")
		return
	}

	sess, err := s.investigationSessionStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get session")
		return
	}

	var activities []store.MCPActivityEvent
	if s.mcpActivityStore != nil {
		activities, _ = s.mcpActivityStore.ListByInvestigationSession(r.Context(), id)
	}

	layout := s.layoutData(r, "Session Detail", "sessions")
	detail := webviews.SessionDetailData{
		Session: sess,
		Events:  activities,
	}

	webviews.SessionDetailPage(layout, detail).Render(r.Context(), w)
}

