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

var templates *template.Template

func init() {
	templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))
}

type pageData struct {
	Title      string
	Nav        string
	Connectors interface{}
}

func (s *Server) handleInvestigatePage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Title: "Investigate",
		Nav:   "investigate",
	}
	templates.ExecuteTemplate(w, "layout", data)
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
		Connectors: connectors,
	}
	templates.ExecuteTemplate(w, "layout", data)
}
