package web

import (
	"embed"
	"html/template"
	"net/http"
	"strconv"

	"github.com/opentrace/opentrace/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var (
	investigateTmpl *template.Template
	connectorsTmpl  *template.Template
	logsTmpl        *template.Template
)

func init() {
	// Each page gets layout + its own content template
	investigateTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/investigate.html"))
	connectorsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/connectors.html"))
	logsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/logs.html"))
}

// templates is used for rendering HTMX fragment responses (e.g. connector-list)
var templates *template.Template

// logsFragmentTmpl is used for rendering HTMX fragment responses for logs-list
var logsFragmentTmpl *template.Template

func init() {
	templates = template.Must(template.ParseFS(templateFS, "templates/connectors.html"))
	logsFragmentTmpl = template.Must(template.ParseFS(templateFS, "templates/logs.html"))
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
	Connectors interface{}
	Logs       []store.LogEntry
	LogFilters LogFilters
}

func (s *Server) handleInvestigatePage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Title:   "Investigate",
		Nav:     "investigate",
		Content: "investigate",
	}
	investigateTmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleLogsPage(w http.ResponseWriter, r *http.Request) {
	filters := LogFilters{
		Query:       r.URL.Query().Get("query"),
		Service:     r.URL.Query().Get("service"),
		Level:       r.URL.Query().Get("level"),
		Environment: r.URL.Query().Get("environment"),
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	logs, err := s.logStore.Search(r.Context(), store.LogSearchParams{
		Query:       filters.Query,
		Service:     filters.Service,
		Level:       filters.Level,
		Environment: filters.Environment,
		Limit:       limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search logs")
		return
	}

	data := pageData{
		Title:      "Logs",
		Nav:        "logs",
		Content:    "logs",
		Logs:       logs,
		LogFilters: filters,
	}

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html")
		logsFragmentTmpl.ExecuteTemplate(w, "logs-list", data)
		return
	}

	logsTmpl.ExecuteTemplate(w, "layout", data)
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
	}
	connectorsTmpl.ExecuteTemplate(w, "layout", data)
}
