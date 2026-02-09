package web

import (
	"embed"
	"html/template"
	"net/http"
	"strconv"

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
	connectorsTmpl  *template.Template
	logsTmpl        *template.Template
	alertsTmpl      *template.Template
	watchersTmpl    *template.Template
	watcherRunsTmpl *template.Template
	setupTmpl       *template.Template
)

func init() {
	// Each page gets layout + its own content template
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

	data := pageData{
		Title:      "Logs",
		Nav:        "logs",
		Content:    "logs",
		Logs:       logs,
		LogFilters: filters,
		LogOffset:  offset,
		LogLimit:   limit,
		HasMore:    hasMore,
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
