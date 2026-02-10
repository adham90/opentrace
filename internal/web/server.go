package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	Router        chi.Router
	dsStore       store.DataSourceStore
	logStore      store.LogStore
	watcherStore  store.WatcherStore
	runStore      store.WatcherRunStore
	alertStore    store.AlertStore
	serverStore   store.ServerStore
	metricStore   store.MetricStore
	registry      *connector.Registry
	cfg           *config.Config
	executor      *watcher.Executor
	eventHub      *watcher.EventHub
	modelRegistry *llm.ModelRegistry
	logsConnMu    sync.Mutex
	metricsConnMu sync.Mutex
}

// ServerDeps holds all dependencies for the web server.
type ServerDeps struct {
	DSStore       store.DataSourceStore
	LogStore      store.LogStore
	WatcherStore  store.WatcherStore
	RunStore      store.WatcherRunStore
	AlertStore    store.AlertStore
	ServerStore   store.ServerStore
	MetricStore   store.MetricStore
	Registry      *connector.Registry
	Cfg           *config.Config
	Executor      *watcher.Executor
	EventHub      *watcher.EventHub
	ModelRegistry *llm.ModelRegistry
}

// NewServer creates a new Server with the given dependencies and sets up routes.
func NewServer(dsStore store.DataSourceStore, logStore store.LogStore, registry *connector.Registry, cfg *config.Config) *Server {
	return NewServerWithDeps(ServerDeps{
		DSStore:  dsStore,
		LogStore: logStore,
		Registry: registry,
		Cfg:      cfg,
	})
}

// NewServerWithDeps creates a new Server using the ServerDeps struct.
func NewServerWithDeps(deps ServerDeps) *Server {
	srv := &Server{
		dsStore:       deps.DSStore,
		logStore:      deps.LogStore,
		watcherStore:  deps.WatcherStore,
		runStore:      deps.RunStore,
		alertStore:    deps.AlertStore,
		serverStore:   deps.ServerStore,
		metricStore:   deps.MetricStore,
		registry:      deps.Registry,
		cfg:           deps.Cfg,
		executor:      deps.Executor,
		eventHub:      deps.EventHub,
		modelRegistry: deps.ModelRegistry,
	}

	cfg := deps.Cfg

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)

	router.Get("/healthz", srv.handleHealthCheck)

	// Static files
	if cfg != nil && cfg.DevMode {
		// Dev mode: serve from disk for live editing
		router.Handle("/static/*", http.StripPrefix("/static/",
			http.FileServer(http.Dir("internal/web/static"))))
	} else {
		staticSub, _ := fs.Sub(staticFS, "static")
		router.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	}

	// Pages
	router.Get("/", srv.handleOverviewPage)
	router.Get("/alerts", srv.handleAlertsPage)
	router.Get("/watchers", srv.handleWatchersPage)
	router.Get("/watchers/{id}/runs", srv.handleWatcherRunsPage)
	router.Get("/logs", srv.handleLogsPage)
	router.Get("/api/logs/poll", srv.handleLogsPoll)
	router.Get("/connectors", srv.handleConnectorsPage)
	router.Get("/servers", srv.handleServersPage)
	router.Get("/servers/{id}", srv.handleServerDetailPage)
	router.Get("/setup", srv.handleSetupPage)

	// API
	router.Route("/api", func(r chi.Router) {
		r.Post("/connectors", srv.handleCreateConnectorAPI)
		r.Get("/connectors", srv.handleListConnectors)
		r.Post("/connectors/{id}/test", srv.handleTestConnectorAPI)
		r.Delete("/connectors/{id}", srv.handleDeleteConnectorAPI)

		// Log ingestion with optional API key auth
		apiKey := ""
		if cfg != nil {
			apiKey = cfg.APIKey
		}
		r.With(APIKeyAuth(apiKey)).Post("/logs", srv.handleIngestLogs)

		// Models API
		r.Get("/models", srv.handleListModels)

		// Watcher API
		r.Post("/watchers", srv.handleCreateWatcher)
		r.Get("/watchers", srv.handleListWatchers)
		r.Get("/watchers/{id}", srv.handleGetWatcher)
		r.Put("/watchers/{id}", srv.handleUpdateWatcher)
		r.Delete("/watchers/{id}", srv.handleDeleteWatcher)
		r.Post("/watchers/{id}/pause", srv.handlePauseWatcher)
		r.Post("/watchers/{id}/resume", srv.handleResumeWatcher)
		r.Post("/watchers/{id}/run", srv.handleRunWatcherNow)
		r.Get("/watchers/{id}/runs", srv.handleListWatcherRuns)
		r.Get("/watchers/{id}/runs/{runId}", srv.handleGetWatcherRun)
		r.Get("/watchers/{id}/runs/{runId}/events", srv.handleRunEvents)

		// Alert API
		r.Get("/alerts", srv.handleListAlerts)
		r.Get("/alerts/count", srv.handleAlertCount)
		r.Post("/alerts/{id}/read", srv.handleMarkAlertRead)
		r.Post("/alerts/{id}/dismiss", srv.handleDismissAlert)

		// Server metrics API
		if srv.serverStore != nil && srv.metricStore != nil {
			r.With(APIKeyAuth(apiKey)).Post("/servers/register", srv.handleRegisterServer)
			r.Get("/servers", srv.handleListServers)
			r.Get("/servers/{id}", srv.handleGetServer)
			r.Delete("/servers/{id}", srv.handleDeleteServer)
			r.With(APIKeyAuth(apiKey)).Post("/servers/{id}/metrics", srv.handlePushMetrics)
			r.Get("/servers/{id}/metrics", srv.handleQueryMetrics)
		}

		// Overview
		r.Get("/overview", srv.handleOverviewAPI)

		// Dev-mode live-reload endpoint
		if cfg != nil && cfg.DevMode {
			r.Get("/dev/hash", srv.handleDevHash)
		}
	})

	srv.Router = router
	return srv
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status": "ok",
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDevHash returns a hash of UI file modification times for live-reload.
func (s *Server) handleDevHash(w http.ResponseWriter, r *http.Request) {
	var buf strings.Builder
	for _, dir := range []string{"internal/web/templates", "internal/web/static"} {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			fmt.Fprintf(&buf, "%s:%d\n", path, info.ModTime().UnixNano())
			return nil
		})
	}
	h := sha256.Sum256([]byte(buf.String()))
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(hex.EncodeToString(h[:8])))
}
