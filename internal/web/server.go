package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	Router        chi.Router
	db            *sql.DB
	dsStore       store.DataSourceStore
	logStore      store.LogStore
	watcherStore  store.WatcherStore
	runStore      store.WatcherRunStore
	alertStore    store.AlertStore
	serverStore   store.ServerStore
	metricStore   store.MetricStore
	userStore     store.UserStore
	sessionStore  store.SessionStore
	settingsStore store.SettingsStore
	digestStore   store.DigestStore
	registry      *connector.Registry
	cfg           *config.Config
	executor      *watcher.Executor
	eventHub      *watcher.EventHub
	modelRegistry *llm.ModelRegistry
	ruleEvaluator *watcher.RuleEvaluator
	toolCatalog   *mcpserver.ToolCatalog
	logsConnMu    sync.Mutex
	metricsConnMu sync.Mutex
}

// ServerDeps holds all dependencies for the web server.
type ServerDeps struct {
	DB            *sql.DB
	DSStore       store.DataSourceStore
	LogStore      store.LogStore
	WatcherStore  store.WatcherStore
	RunStore      store.WatcherRunStore
	AlertStore    store.AlertStore
	ServerStore   store.ServerStore
	MetricStore   store.MetricStore
	UserStore     store.UserStore
	SessionStore  store.SessionStore
	SettingsStore store.SettingsStore
	DigestStore   store.DigestStore
	Registry      *connector.Registry
	ToolCatalog   *mcpserver.ToolCatalog
	Cfg           *config.Config
	Executor      *watcher.Executor
	EventHub      *watcher.EventHub
	ModelRegistry *llm.ModelRegistry
	RuleEvaluator *watcher.RuleEvaluator
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
		db:            deps.DB,
		dsStore:       deps.DSStore,
		logStore:      deps.LogStore,
		watcherStore:  deps.WatcherStore,
		runStore:      deps.RunStore,
		alertStore:    deps.AlertStore,
		serverStore:   deps.ServerStore,
		metricStore:   deps.MetricStore,
		userStore:     deps.UserStore,
		sessionStore:  deps.SessionStore,
		settingsStore: deps.SettingsStore,
		digestStore:   deps.DigestStore,
		registry:      deps.Registry,
		toolCatalog:   deps.ToolCatalog,
		cfg:           deps.Cfg,
		executor:      deps.Executor,
		eventHub:      deps.EventHub,
		modelRegistry: deps.ModelRegistry,
		ruleEvaluator: deps.RuleEvaluator,
	}

	cfg := deps.Cfg

	loginLimiter := NewRateLimiter(10, 1*time.Minute)
	apiLimiter := NewRateLimiter(120, 1*time.Minute)

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)
	router.Use(SecurityHeaders)
	router.Use(MaxBodySize(10 << 20)) // 10 MB global body limit
	router.Use(srv.SessionAuth)

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

	// Always-open auth routes (login/register rate-limited)
	router.Get("/login", srv.handleLoginPage)
	router.With(loginLimiter.Middleware).Post("/login", srv.handleLoginSubmit)
	router.Get("/register", srv.handleRegisterPage)
	router.With(loginLimiter.Middleware).Post("/register", srv.handleRegisterSubmit)
	router.Post("/logout", srv.handleLogout)

	// Onboarding routes (open — guarded inside handler)
	router.Get("/onboarding", srv.handleOnboardingPage)
	router.Post("/onboarding", srv.handleOnboardingSubmit)

	// Pages — require auth, redirect to onboarding if no users
	router.Group(func(r chi.Router) {
		r.Use(srv.RedirectToOnboardingIfNeeded)
		r.Get("/", srv.handleOverviewPage)
		r.Get("/alerts", srv.handleAlertsPage)
		r.Get("/watchers", srv.handleWatchersPage)
		r.Get("/watchers/{id}/runs", srv.handleWatcherRunsPage)
		r.Get("/logs", srv.handleLogsPage)
		r.Get("/connectors", srv.handleConnectorsPage)
		r.Get("/servers", srv.handleServersPage)
		r.Get("/servers/{id}", srv.handleServerDetailPage)
		r.Get("/setup", srv.handleSetupPage)
		r.Get("/tools", srv.handleToolsPage)
		r.Get("/digests", srv.handleDigestsPage)
		r.Get("/profile", srv.handleProfilePage)
	})

	// Admin pages
	router.Group(func(r chi.Router) {
		r.Use(srv.RedirectToOnboardingIfNeeded)
		r.Use(RequireAdmin)
		r.Get("/admin/users", srv.handleUsersPage)
		r.Get("/admin/settings", srv.handleSettingsPage)
	})

	// API
	router.Route("/api", func(r chi.Router) {
		// Agent install script (no auth — the script is self-contained)
		r.Get("/agent/install.sh", srv.handleAgentInstallScript)

		// Log ingestion with dynamic API key auth + rate limiting
		r.With(apiLimiter.Middleware, srv.DynamicAPIKeyAuth).Post("/logs", srv.handleIngestLogs)

		// Server registration and metric push with dynamic API key auth + rate limiting
		if srv.serverStore != nil && srv.metricStore != nil {
			r.With(apiLimiter.Middleware, srv.DynamicAPIKeyAuth).Post("/servers/register", srv.handleRegisterServer)
			r.With(apiLimiter.Middleware, srv.DynamicAPIKeyAuth).Post("/servers/{id}/metrics", srv.handlePushMetrics)
		}

		// Read API — require auth, 503 if onboarding needed
		r.Group(func(r chi.Router) {
			r.Use(srv.requireAuthOrOnboardingAPI)
			r.Get("/environments", srv.handleListEnvironments)
			r.Get("/services", srv.handleListServices)
			r.Get("/connectors", srv.handleListConnectors)
			r.Get("/models", srv.handleListModels)
			r.Get("/watchers", srv.handleListWatchers)
			r.Get("/watchers/{id}", srv.handleGetWatcher)
			r.Get("/watchers/{id}/runs", srv.handleListWatcherRuns)
			r.Get("/watchers/{id}/runs/{runId}", srv.handleGetWatcherRun)
			r.Get("/watchers/{id}/runs/{runId}/events", srv.handleRunEvents)
			r.Get("/monitors/templates", srv.handleMonitorTemplates)
			r.Get("/alerts", srv.handleListAlerts)
			r.Get("/alerts/count", srv.handleAlertCount)
			r.Get("/overview", srv.handleOverviewAPI)
			r.Get("/tools", srv.handleToolsAPI)
			r.Get("/logs/poll", srv.handleLogsPoll)
			r.Get("/digests/latest", srv.handleGetLatestDigest)
			r.Get("/digests", srv.handleListDigests)

			if srv.serverStore != nil && srv.metricStore != nil {
				r.Get("/servers", srv.handleListServers)
				r.Get("/servers/{id}", srv.handleGetServer)
				r.Get("/servers/{id}/metrics", srv.handleQueryMetrics)
			}
		})

		// Write API — require admin, 503 if onboarding needed
		r.Group(func(r chi.Router) {
			r.Use(srv.requireAdminOrOnboarding)
			r.Post("/connectors", srv.handleCreateConnectorAPI)
			r.Post("/connectors/{id}/test", srv.handleTestConnectorAPI)
			r.Delete("/connectors/{id}", srv.handleDeleteConnectorAPI)
			r.Post("/watchers", srv.handleCreateWatcher)
			r.Put("/watchers/{id}", srv.handleUpdateWatcher)
			r.Delete("/watchers/{id}", srv.handleDeleteWatcher)
			r.Post("/watchers/{id}/pause", srv.handlePauseWatcher)
			r.Post("/watchers/{id}/resume", srv.handleResumeWatcher)
			r.Post("/watchers/{id}/run", srv.handleRunWatcherNow)
			r.Post("/watchers/{id}/runs/{runId}/stop", srv.handleStopRun)
			r.Post("/monitors/preview", srv.handleMonitorPreview)
			r.Post("/alerts/read-all", srv.handleMarkAllAlertsRead)
			r.Post("/alerts/dismiss-all", srv.handleDismissAllAlerts)
			r.Post("/alerts/{id}/read", srv.handleMarkAlertRead)
			r.Post("/alerts/{id}/dismiss", srv.handleDismissAlert)

			if srv.serverStore != nil && srv.metricStore != nil {
				r.Delete("/servers/{id}", srv.handleDeleteServer)
			}
		})

		// Settings API (admin only)
		r.Group(func(r chi.Router) {
			r.Use(srv.requireAdminOrOnboarding)
			r.Get("/settings/retention", srv.handleGetRetention)
			r.Put("/settings/retention", srv.handleUpdateRetention)
			r.Get("/settings/api-key", srv.handleGetAPIKey)
			r.Post("/settings/api-key", srv.handleRegenerateAPIKey)
		})

		// User management API (admin only)
		r.Group(func(r chi.Router) {
			r.Use(srv.requireAdminOrOnboarding)
			r.Post("/users/{id}/role", srv.handleUpdateUserRole)
			r.Post("/users/{id}/mcp", srv.handleToggleMCPAccess)
			r.Post("/users/{id}/active", srv.handleToggleUserActive)
			r.Post("/users/{id}/mcp-token", srv.handleRegenerateMCPToken)
			r.Delete("/users/{id}", srv.handleDeleteUser)
		})

		// Profile API (auth required)
		r.Group(func(r chi.Router) {
			r.Use(srv.requireAuthOrOnboardingAPI)
			r.Post("/profile/password", srv.handleChangePassword)
		})

		// Dev-mode live-reload endpoint
		if cfg != nil && cfg.DevMode {
			r.Get("/dev/hash", srv.handleDevHash)
		}
	})

	srv.Router = router
	return srv
}

// getEffectiveAPIKey returns the API key from the env var (if set) or from the DB.
func (s *Server) getEffectiveAPIKey(ctx context.Context) string {
	if s.cfg != nil && s.cfg.APIKey != "" {
		return s.cfg.APIKey
	}
	if s.settingsStore != nil {
		key, err := s.settingsStore.GetAPIKey(ctx)
		if err == nil && key != "" {
			return key
		}
	}
	return ""
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
