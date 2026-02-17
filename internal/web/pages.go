package web

import (
	"embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var tmplFuncs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
}

var (
	dashboardTmpl  *template.Template
	loginTmpl      *template.Template
	registerTmpl   *template.Template
	profileTmpl    *template.Template
	settingsTmpl   *template.Template
	onboardingTmpl *template.Template
)

func init() {
	// Each page gets layout + its own content template
	dashboardTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/dashboard.html"))
	loginTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/login.html"))
	registerTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/register.html"))
	profileTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/profile.html"))
	settingsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/settings.html"))
	onboardingTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout_minimal.html", "templates/onboarding.html"))
}

// logsFragmentTmpl is used for rendering HTMX fragment responses for logs-list
var logsFragmentTmpl *template.Template

func init() {
	logsFragmentTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS, "templates/logs.html"))
}

type LogFilters struct {
	Query            string
	Service          string
	Level            string
	EventType        string
	TimeRange        string // preset: 15m, 1h, 6h, 24h, 7d, or empty (all)
	Environment      string
	CommitHash       string
	RequestID        string
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

type pageData struct {
	Title          string
	Nav            string
	ServerID       string
	DevMode        bool
	CSPNonce       string
	Connectors     interface{}
	Logs           []store.LogEntry
	LogFilters     LogFilters
	LogOffset      int
	LogLimit       int
	HasMore        bool
	MaxLogID       int64
	User           *store.User
	Users          []store.User
	IsAdmin        bool
	RetentionDays  int
	APIKey          string
	EnvKeyOverride  bool
	CORSOrigins     string
	CORSEnvOverride bool
}

func (s *Server) isDevMode() bool {
	return s.cfg != nil && s.cfg.DevMode
}

// newPageData creates a pageData with common fields populated from the request context.
func (s *Server) newPageData(r *http.Request, title, nav string) pageData {
	user := UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	var apiKey string
	if isAdmin {
		apiKey = s.getEffectiveAPIKey(r.Context())
	}
	return pageData{
		Title:    title,
		Nav:      nav,
		DevMode:  s.isDevMode(),
		CSPNonce: CSPNonce(r.Context()),
		User:     user,
		IsAdmin:  isAdmin,
		APIKey:   apiKey,
	}
}

// getTemplate returns a freshly-parsed template from disk in dev mode,
// or the pre-compiled embedded template in production.
func (s *Server) getTemplate(fallback *template.Template, files ...string) *template.Template {
	if !s.isDevMode() {
		return fallback
	}
	t, err := template.New("").Funcs(tmplFuncs).ParseFiles(files...)
	if err != nil {
		return fallback
	}
	return t
}

func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
	filters := LogFilters{
		Query:            r.URL.Query().Get("query"),
		Service:          r.URL.Query().Get("service"),
		Level:            r.URL.Query().Get("level"),
		EventType:        r.URL.Query().Get("event_type"),
		TimeRange:        r.URL.Query().Get("time_range"),
		Environment:      r.URL.Query().Get("environment"),
		CommitHash:       r.URL.Query().Get("commit_hash"),
		RequestID:        r.URL.Query().Get("request_id"),
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
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
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

	var logs []store.LogEntry
	var hasMore bool
	var maxID int64
	if s.logStore != nil {
		var err error
		logs, err = s.logStore.Search(r.Context(), searchParams)
		if err != nil {
			logs = nil
		}
		hasMore = len(logs) > limit
		if hasMore {
			logs = logs[:limit]
		}
		for _, l := range logs {
			if l.ID > maxID {
				maxID = l.ID
			}
		}
	}

	data := s.newPageData(r, "Dashboard", "dashboard")
	data.Logs = logs
	data.LogFilters = filters
	data.LogOffset = offset
	data.LogLimit = limit
	data.HasMore = hasMore
	data.MaxLogID = maxID

	tmpl := s.getTemplate(dashboardTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/dashboard.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

// handleLogsFragment serves HTMX log fragments for the dashboard.
// Browser visits to /logs redirect to the dashboard.
func (s *Server) handleLogsFragment(w http.ResponseWriter, r *http.Request) {
	if !isHTMX(r) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
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
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
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

	data := pageData{
		Logs:       logs,
		LogFilters: filters,
		LogOffset:  offset,
		LogLimit:   limit,
		HasMore:    hasMore,
		MaxLogID:   maxID,
	}

	w.Header().Set("Content-Type", "text/html")
	ft := s.getTemplate(logsFragmentTmpl, "internal/web/templates/logs.html")
	if offset > 0 {
		ft.ExecuteTemplate(w, "logs-rows", data)
	} else {
		ft.ExecuteTemplate(w, "logs-list", data)
	}
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
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
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

	ft := s.getTemplate(logsFragmentTmpl, "internal/web/templates/logs.html")
	ft.ExecuteTemplate(w, "logs-new", pageData{Logs: logs, MaxLogID: maxID})
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


func (s *Server) handleOverviewAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type overviewStats struct {
		Logs       map[string]int `json:"logs"`
		Connectors map[string]int `json:"connectors"`
		Servers    map[string]int `json:"servers"`
	}

	stats := overviewStats{
		Logs:       map[string]int{"last_hour": 0, "errors_last_hour": 0},
		Connectors: map[string]int{"total": 0, "connected": 0, "error": 0},
		Servers:    map[string]int{"total": 0, "online": 0, "offline": 0},
	}

	// Logs stats (last hour) — use COUNT query instead of fetching rows
	if s.logStore != nil {
		now := time.Now()
		logOneHourAgo := now.Add(-1 * time.Hour)
		counts, err := s.logStore.CountByLevel(ctx, store.LogCountParams{
			Since: logOneHourAgo,
			Until: now,
		})
		if err == nil {
			total := 0
			for level, count := range counts {
				total += count
				if level == "ERROR" {
					stats.Logs["errors_last_hour"] = count
				}
			}
			stats.Logs["last_hour"] = total
		}
	}

	// Connectors stats
	if s.dsStore != nil {
		connectors, err := s.dsStore.List(ctx, store.ListDataSourceParams{})
		if err == nil {
			stats.Connectors["total"] = len(connectors)
			for _, c := range connectors {
				switch c.Status {
				case store.StatusConnected:
					stats.Connectors["connected"]++
				case store.StatusError:
					stats.Connectors["error"]++
				}
			}
		}
	}

	// Servers stats
	if s.serverStore != nil {
		servers, err := s.serverStore.List(ctx)
		if err == nil {
			stats.Servers["total"] = len(servers)
			for _, srv := range servers {
				switch srv.Status {
				case store.ServerOnline:
					stats.Servers["online"]++
				case store.ServerOffline:
					stats.Servers["offline"]++
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, stats)
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

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Settings", "settings")
	if s.settingsStore != nil {
		settings, err := s.settingsStore.GetRetention(r.Context())
		if err == nil {
			data.RetentionDays = settings.RetentionDays
		} else {
			data.RetentionDays = 30
		}
	} else {
		data.RetentionDays = 30
	}
	data.EnvKeyOverride = s.cfg != nil && s.cfg.APIKey != ""
	data.CORSEnvOverride = s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0
	if data.CORSEnvOverride {
		data.CORSOrigins = strings.Join(s.cfg.CORSAllowedOrigins, ",")
	} else if s.settingsStore != nil {
		if val, err := s.settingsStore.GetCORSOrigins(r.Context()); err == nil {
			data.CORSOrigins = val
		}
	}

	// Load users for user management tab
	if s.userStore != nil {
		users, err := s.userStore.List(r.Context())
		if err == nil {
			data.Users = users
		}
	}

	tmpl := s.getTemplate(settingsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/settings.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

