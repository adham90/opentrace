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
	overviewTmpl    *template.Template
	connectorsTmpl  *template.Template
	logsTmpl        *template.Template
	alertsTmpl      *template.Template
	watchersTmpl    *template.Template
	watcherRunsTmpl *template.Template
	setupTmpl       *template.Template
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
}

type pageData struct {
	Title      string
	Nav        string
	Content    string
	WatcherID  string
	DevMode    bool
	Connectors interface{}
	Logs       []store.LogEntry
	LogFilters LogFilters
	LogOffset  int
	LogLimit   int
	HasMore    bool
	MaxLogID   int64
}

func (s *Server) isDevMode() bool {
	return s.cfg != nil && s.cfg.DevMode
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
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
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

	data := pageData{
		Title:      "Logs",
		Nav:        "logs",
		Content:    "logs",
		Logs:       logs,
		LogFilters: filters,
		LogOffset:  offset,
		LogLimit:   limit,
		HasMore:    hasMore,
		MaxLogID:   maxID,
		DevMode:    s.isDevMode(),
	}

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
	}

	logs, err := s.logStore.Search(r.Context(), store.LogSearchParams{
		Query:       filters.Query,
		Service:     filters.Service,
		Level:       filters.Level,
		Environment: filters.Environment,
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
	data := pageData{
		Title:   "Alerts",
		Nav:     "alerts",
		DevMode: s.isDevMode(),
	}
	tmpl := s.getTemplate(alertsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/alerts.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleWatchersPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Title:   "Watchers",
		Nav:     "watchers",
		DevMode: s.isDevMode(),
	}
	tmpl := s.getTemplate(watchersTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/watchers.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleWatcherRunsPage(w http.ResponseWriter, r *http.Request) {
	watcherID := chi.URLParam(r, "id")
	data := pageData{
		Title:     "Watcher Runs",
		Nav:       "watchers",
		WatcherID: watcherID,
		DevMode:   s.isDevMode(),
	}
	tmpl := s.getTemplate(watcherRunsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/watcher_runs.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Title:   "Setup",
		Nav:     "setup",
		DevMode: s.isDevMode(),
	}
	tmpl := s.getTemplate(setupTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/setup.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleConnectorsPage(w http.ResponseWriter, r *http.Request) {
	connectors, err := s.dsStore.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list connectors")
		return
	}

	data := pageData{
		Title:      "Connectors",
		Nav:        "connectors",
		Content:    "connectors",
		Connectors: connectors,
		DevMode:    s.isDevMode(),
	}
	tmpl := s.getTemplate(connectorsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/connectors.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleOverviewPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Title:   "Overview",
		Nav:     "overview",
		DevMode: s.isDevMode(),
	}
	tmpl := s.getTemplate(overviewTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/overview.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleOverviewAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type overviewStats struct {
		Alerts     map[string]int `json:"alerts"`
		Watchers   map[string]int `json:"watchers"`
		Logs       map[string]int `json:"logs"`
		Connectors map[string]int `json:"connectors"`
	}

	stats := overviewStats{
		Alerts:     map[string]int{"total": 0, "critical": 0, "warning": 0, "info": 0},
		Watchers:   map[string]int{"total": 0, "active": 0, "paused": 0, "error": 0},
		Logs:       map[string]int{"last_hour": 0, "errors_last_hour": 0},
		Connectors: map[string]int{"total": 0, "connected": 0, "error": 0},
	}

	// Alerts stats
	if s.alertStore != nil {
		alerts, err := s.alertStore.List(ctx, store.ListAlertParams{Limit: 500})
		if err == nil {
			for _, a := range alerts {
				if !a.Dismissed {
					stats.Alerts["total"]++
					switch a.Severity {
					case store.SeverityCritical:
						stats.Alerts["critical"]++
					case store.SeverityWarning:
						stats.Alerts["warning"]++
					case store.SeverityInfo:
						stats.Alerts["info"]++
					}
				}
			}
		}
	}

	// Watchers stats
	if s.watcherStore != nil {
		watchers, err := s.watcherStore.List(ctx)
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

	// Logs stats (last hour)
	if s.logStore != nil {
		oneHourAgo := time.Now().Add(-1 * time.Hour)
		logs, err := s.logStore.Search(ctx, store.LogSearchParams{
			Start: &oneHourAgo,
			Limit: 10000,
		})
		if err == nil {
			stats.Logs["last_hour"] = len(logs)
			for _, l := range logs {
				if l.Level == "ERROR" {
					stats.Logs["errors_last_hour"]++
				}
			}
		}
	}

	// Connectors stats
	if s.dsStore != nil {
		connectors, err := s.dsStore.List(ctx)
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

	writeJSON(w, http.StatusOK, stats)
}
