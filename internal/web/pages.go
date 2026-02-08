package web

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var (
	investigateTmpl *template.Template
	connectorsTmpl  *template.Template
)

func init() {
	// Each page gets layout + its own content template
	investigateTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/investigate.html"))
	connectorsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/connectors.html"))
}

// templates is used for rendering HTMX fragment responses (e.g. connector-list)
var templates *template.Template

func init() {
	templates = template.Must(template.ParseFS(templateFS, "templates/connectors.html"))
}

type pageData struct {
	Title      string
	Nav        string
	Content    string
	Connectors interface{}
}

func (s *Server) handleInvestigatePage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Title:   "Investigate",
		Nav:     "investigate",
		Content: "investigate",
	}
	investigateTmpl.ExecuteTemplate(w, "layout", data)
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
