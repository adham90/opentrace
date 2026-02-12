package web

import (
	"embed"
	"html/template"
	"net/http"
	"strconv"
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
	overviewTmpl       *template.Template
	connectorsTmpl     *template.Template
	logsTmpl           *template.Template
	alertsTmpl         *template.Template
	watchersTmpl       *template.Template
	watcherRunsTmpl    *template.Template
	setupTmpl          *template.Template
	serversTmpl        *template.Template
	serverDetailTmpl   *template.Template
	loginTmpl          *template.Template
	registerTmpl       *template.Template
	profileTmpl        *template.Template
	usersTmpl          *template.Template
	settingsTmpl       *template.Template
	toolsTmpl          *template.Template
	onboardingTmpl     *template.Template
)

func init() {
	// Each page gets layout + its own content template
	overviewTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/overview.html"))
	connectorsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/connectors.html"))
	logsTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/logs.html"))
	alertsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/alerts.html"))
	watchersTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/watchers.html"))
	watcherRunsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/watcher_runs.html"))
	setupTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/setup.html"))
	serversTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/servers.html"))
	serverDetailTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/server_detail.html"))
	loginTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/login.html"))
	registerTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/register.html"))
	profileTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/profile.html"))
	usersTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/users.html"))
	settingsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/settings.html"))
	toolsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/tools.html"))
	onboardingTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout_minimal.html", "templates/onboarding.html"))
}

// templates is used for rendering HTMX fragment responses (e.g. connector-list)
var templates *template.Template

// logsFragmentTmpl is used for rendering HTMX fragment responses for logs-list
var logsFragmentTmpl *template.Template

func init() {
	templates = template.Must(template.ParseFS(templateFS, "templates/connectors.html"))
	logsFragmentTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS, "templates/logs.html"))
}

type LogFilters struct {
	Query       string
	Service     string
	Level       string
	Environment string
	EventType   string
	TimeRange   string // preset: 15m, 1h, 6h, 24h, 7d, or empty (all)
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

// Breadcrumb represents a single navigation breadcrumb.
type Breadcrumb struct {
	Label string
	URL   string // empty for the last (current) item
}

type pageData struct {
	Title          string
	Nav            string
	Content        string
	WatcherID      string
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
	IsAdmin        bool
	RetentionDays  int
	APIKey         string
	EnvKeyOverride bool
	Breadcrumbs    []Breadcrumb
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

func (s *Server) handleLogsPage(w http.ResponseWriter, r *http.Request) {
	filters := LogFilters{
		Query:       r.URL.Query().Get("query"),
		Service:     r.URL.Query().Get("service"),
		Level:       r.URL.Query().Get("level"),
		Environment: r.URL.Query().Get("environment"),
		EventType:   r.URL.Query().Get("event_type"),
		TimeRange:   r.URL.Query().Get("time_range"),
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

	logs, err := s.logStore.Search(r.Context(), store.LogSearchParams{
		Query:       filters.Query,
		Service:     filters.Service,
		Level:       filters.Level,
		Environment: filters.Environment,
		EventType:   filters.EventType,
		Start:       parseTimeRange(filters.TimeRange),
		Limit:       limit + 1, // fetch one extra to detect if there are more
		Offset:      offset,
	})
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

	data := s.newPageData(r, "Logs", "logs")
	data.Content = "logs"
	data.Logs = logs
	data.LogFilters = filters
	data.LogOffset = offset
	data.LogLimit = limit
	data.HasMore = hasMore
	data.MaxLogID = maxID

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html")
		ft := s.getTemplate(logsFragmentTmpl, "internal/web/templates/logs.html")
		// If this is a "load more" request (has offset), return just the rows
		if offset > 0 {
			ft.ExecuteTemplate(w, "logs-rows", data)
		} else {
			ft.ExecuteTemplate(w, "logs-list", data)
		}
		return
	}

	tmpl := s.getTemplate(logsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/logs.html")
	tmpl.ExecuteTemplate(w, "layout", data)
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
		Query:       r.URL.Query().Get("query"),
		Service:     r.URL.Query().Get("service"),
		Level:       r.URL.Query().Get("level"),
		Environment: r.URL.Query().Get("environment"),
		EventType:   r.URL.Query().Get("event_type"),
		TimeRange:   r.URL.Query().Get("time_range"),
	}

	logs, err := s.logStore.Search(r.Context(), store.LogSearchParams{
		Query:       filters.Query,
		Service:     filters.Service,
		Level:       filters.Level,
		Environment: filters.Environment,
		EventType:   filters.EventType,
		Start:       parseTimeRange(filters.TimeRange),
		SinceID:     sinceID,
		Limit:       200,
	})
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

func (s *Server) handleAlertsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Alerts", "alerts")
	tmpl := s.getTemplate(alertsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/alerts.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleWatchersPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Watchers", "watchers")
	tmpl := s.getTemplate(watchersTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/watchers.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleWatcherRunsPage(w http.ResponseWriter, r *http.Request) {
	watcherID := chi.URLParam(r, "id")
	data := s.newPageData(r, "Watcher Runs", "watchers")
	data.WatcherID = watcherID
	tmpl := s.getTemplate(watcherRunsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/watcher_runs.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleServersPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Servers", "servers")
	tmpl := s.getTemplate(serversTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/servers.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleServerDetailPage(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	data := s.newPageData(r, "Server", "servers")
	data.ServerID = serverID
	tmpl := s.getTemplate(serverDetailTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/server_detail.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Setup", "setup")
	tmpl := s.getTemplate(setupTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/setup.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleToolsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Tools", "tools")
	tmpl := s.getTemplate(toolsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/tools.html")
	tmpl.ExecuteTemplate(w, "layout", data)
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

func (s *Server) handleConnectorsPage(w http.ResponseWriter, r *http.Request) {
	connectors, err := s.dsStore.List(r.Context(), store.ListDataSourceParams{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list connectors")
		return
	}

	data := s.newPageData(r, "Connectors", "connectors")
	data.Content = "connectors"
	data.Connectors = connectors
	tmpl := s.getTemplate(connectorsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/connectors.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleOverviewPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Overview", "overview")
	tmpl := s.getTemplate(overviewTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/overview.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleOverviewAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	env := r.URL.Query().Get("env")

	type overviewStats struct {
		Alerts     map[string]int `json:"alerts"`
		Watchers   map[string]int `json:"watchers"`
		Logs       map[string]int `json:"logs"`
		Connectors map[string]int `json:"connectors"`
		Servers    map[string]int `json:"servers"`
	}

	stats := overviewStats{
		Alerts:     map[string]int{"total": 0, "critical": 0, "warning": 0, "info": 0},
		Watchers:   map[string]int{"total": 0, "active": 0, "paused": 0, "error": 0},
		Logs:       map[string]int{"last_hour": 0, "errors_last_hour": 0},
		Connectors: map[string]int{"total": 0, "connected": 0, "error": 0},
		Servers:    map[string]int{"total": 0, "online": 0, "offline": 0},
	}

	// Alerts stats — use aggregate counts instead of fetching all rows
	if s.alertStore != nil {
		if total, err := s.alertStore.CountTotal(ctx, env); err == nil {
			stats.Alerts["total"] = total
		}
		oneHourAgo := time.Now().Add(-1 * time.Hour)
		if bySev, err := s.alertStore.CountBySeverity(ctx, oneHourAgo, time.Now(), env); err == nil {
			for sev, count := range bySev {
				stats.Alerts[sev] = count
			}
		}
	}

	// Watchers stats
	if s.watcherStore != nil {
		watchers, err := s.watcherStore.List(ctx, store.ListWatcherParams{Environment: env})
		if err == nil {
			stats.Watchers["total"] = len(watchers)
			for _, w := range watchers {
				switch w.Status {
				case store.WatcherActive:
					stats.Watchers["active"]++
				case store.WatcherPaused:
					stats.Watchers["paused"]++
				case store.WatcherError:
					stats.Watchers["error"]++
				}
			}
		}
	}

	// Logs stats (last hour) — use COUNT query instead of fetching rows
	if s.logStore != nil {
		now := time.Now()
		logOneHourAgo := now.Add(-1 * time.Hour)
		counts, err := s.logStore.CountByLevel(ctx, store.LogCountParams{
			Since:       logOneHourAgo,
			Until:       now,
			Environment: env,
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
		connectors, err := s.dsStore.List(ctx, store.ListDataSourceParams{Environment: env})
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

func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	envs, err := store.ListEnvironments(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list environments")
		return
	}
	if envs == nil {
		envs = []string{}
	}
	writeJSON(w, http.StatusOK, envs)
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
	tmpl := s.getTemplate(settingsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/settings.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}
