package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/store"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	Router   chi.Router
	store    store.DataSourceStore
	registry *connector.Registry
}

// NewServer creates a new Server with the given dependencies and sets up routes.
func NewServer(s store.DataSourceStore, r *connector.Registry) *Server {
	srv := &Server{
		store:    s,
		registry: r,
	}

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)

	router.Get("/healthz", srv.handleHealthCheck)

	router.Route("/api", func(r chi.Router) {
		r.Post("/connectors", srv.handleCreateConnector)
		r.Get("/connectors", srv.handleListConnectors)
		r.Post("/connectors/{id}/test", srv.handleTestConnector)
		r.Delete("/connectors/{id}", srv.handleDeleteConnector)
		r.Get("/sse/demo", srv.handleSSEDemo)
	})

	srv.Router = router
	return srv
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
