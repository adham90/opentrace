package web

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/opentrace/opentrace/internal/config"
	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/llm"
	"github.com/opentrace/opentrace/internal/store"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	Router   chi.Router
	dsStore  store.DataSourceStore
	logStore store.LogStore
	embStore store.EmbeddingStore
	registry *connector.Registry
	cfg      *config.Config
	embedder llm.EmbeddingProvider
}

// NewServer creates a new Server with the given dependencies and sets up routes.
func NewServer(dsStore store.DataSourceStore, logStore store.LogStore, embStore store.EmbeddingStore, registry *connector.Registry, cfg *config.Config, embedder llm.EmbeddingProvider) *Server {
	srv := &Server{
		dsStore:  dsStore,
		logStore: logStore,
		embStore: embStore,
		registry: registry,
		cfg:      cfg,
		embedder: embedder,
	}

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)

	router.Get("/healthz", srv.handleHealthCheck)

	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	router.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Pages
	router.Get("/", srv.handleInvestigatePage)
	router.Get("/connectors", srv.handleConnectorsPage)

	// API
	router.Route("/api", func(r chi.Router) {
		r.Post("/connectors", srv.handleCreateConnectorAPI)
		r.Get("/connectors", srv.handleListConnectors)
		r.Post("/connectors/{id}/test", srv.handleTestConnectorAPI)
		r.Delete("/connectors/{id}", srv.handleDeleteConnectorAPI)
		r.Post("/logs", srv.handleIngestLogs)
		r.Get("/investigate", srv.handleInvestigateSSE)
		r.Get("/sse/demo", srv.handleSSEDemo)
	})

	srv.Router = router
	return srv
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
