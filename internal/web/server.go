package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/watcher"
)

const (
	// APIVersion is the current ingestion API version. Increment on breaking changes.
	APIVersion = 1
	// MinClientAPIVersion is the minimum client API version the server accepts.
	MinClientAPIVersion = 1
)

// serverCapabilities advertises features the server supports.
// Clients use this for capability-based feature negotiation.
var serverCapabilities = []string{
	"batch_ingest",
	"gzip_request",
	"batch_dedup",
	"request_summaries",
	"trace_context",
}

// Server holds the HTTP server and its dependencies.
type Server struct {
	Router        chi.Router
	db            *sql.DB
	dsStore       store.DataSourceStore
	logStore      store.LogStore
	serverStore   store.ServerStore
	metricStore   store.MetricStore
	userStore     store.UserStore
	sessionStore  store.SessionStore
	settingsStore store.SettingsStore
	registry      *connector.Registry
	cfg           *config.Config
	toolCatalog      *mcpserver.ToolCatalog
	mcpActivityStore store.MCPActivityStore
	auditStore       store.AuditStore
	watchStream      *watcher.WatchStreamEvaluator
	watchStore       store.WatchStore
	watchMetrics     *watcher.WatchMetrics
	errorGroupStore    store.ErrorGroupStore
	healthCheckStore   store.HealthCheckStore
	agentNoteStore     store.AgentNoteStore
	trendStore         store.TrendStore
	analyticsStore     store.AnalyticsStore
	journeyStore       store.JourneyStore
	versionChecker     *versionChecker
	sseServer      *mcpgoserver.SSEServer
	loginLimiter   *RateLimiter
	apiLimiter     *RateLimiter
	loginTracker   *loginTracker
	secureCookies  bool
	logsConnMu     sync.Mutex
	metricsConnMu  sync.Mutex
}

// ServerDeps holds all dependencies for the web server.
type ServerDeps struct {
	DB            *sql.DB
	DSStore       store.DataSourceStore
	LogStore      store.LogStore
	ServerStore   store.ServerStore
	MetricStore   store.MetricStore
	UserStore     store.UserStore
	SessionStore  store.SessionStore
	SettingsStore store.SettingsStore
	Registry      *connector.Registry
	ToolCatalog   *mcpserver.ToolCatalog
	Cfg           *config.Config
	MCPActivityStore store.MCPActivityStore
	AuditStore       store.AuditStore
	WatchStreamEvaluator *watcher.WatchStreamEvaluator
	WatchStore           store.WatchStore
	WatchMetrics         *watcher.WatchMetrics
	ErrorGroupStore      store.ErrorGroupStore
	HealthCheckStore     store.HealthCheckStore
	AgentNoteStore       store.AgentNoteStore
	TrendStore           store.TrendStore
	AnalyticsStore       store.AnalyticsStore
	JourneyStore         store.JourneyStore
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
		serverStore:   deps.ServerStore,
		metricStore:   deps.MetricStore,
		userStore:     deps.UserStore,
		sessionStore:  deps.SessionStore,
		settingsStore: deps.SettingsStore,
		registry:      deps.Registry,
		toolCatalog:   deps.ToolCatalog,
		cfg:           deps.Cfg,
		mcpActivityStore: deps.MCPActivityStore,
		auditStore:       deps.AuditStore,
		watchStream:      deps.WatchStreamEvaluator,
		watchStore:       deps.WatchStore,
		watchMetrics:     deps.WatchMetrics,
		errorGroupStore:    deps.ErrorGroupStore,
		healthCheckStore:   deps.HealthCheckStore,
		agentNoteStore:     deps.AgentNoteStore,
		trendStore:         deps.TrendStore,
		analyticsStore:     deps.AnalyticsStore,
		journeyStore:       deps.JourneyStore,
		versionChecker:     newVersionChecker("adham90", "opentrace"),
	}

	cfg := deps.Cfg

	var trustedProxies []string
	if cfg != nil {
		trustedProxies = cfg.TrustedProxies
	}
	srv.loginLimiter = NewRateLimiter(10, 1*time.Minute, trustedProxies)
	srv.apiLimiter = NewRateLimiter(120, 1*time.Minute, trustedProxies)
	srv.loginTracker = newLoginTracker()
	// Use secure cookies unless in dev mode or listening on localhost
	srv.secureCookies = true
	if cfg != nil {
		addr := cfg.ListenAddr
		if cfg.DevMode || strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:") {
			srv.secureCookies = false
		}
	}
	loginLimiter := srv.loginLimiter
	apiLimiter := srv.apiLimiter

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)
	router.Use(SecurityHeaders)
	router.Use(skipCompressForSSE)     // must come before Compress — disables gzip for SSE
	router.Use(middleware.Compress(5)) // gzip compression
	router.Use(MaxBodySize(10 << 20))  // 10 MB global body limit
	router.Use(srv.SessionAuth)        // skips /static/ and /healthz paths internally

	router.Get("/healthz", srv.handleHealthCheck)
	router.Get("/api/version", srv.handleVersion)
	router.Get("/api/version/check", srv.handleVersionCheck)
	router.Get("/api/version/banner", srv.handleVersionBanner)

	// MCP SSE transport — authenticated via Bearer token (MCP token).
	if srv.userStore != nil && srv.registry != nil {
		sseServer := srv.setupMCPSSE()
		srv.sseServer = sseServer
		router.Route("/mcp", func(r chi.Router) {
			r.Use(apiLimiter.Middleware)
			r.Use(srv.MCPTokenAuth)
			r.Handle("/sse", sseServer.SSEHandler())
			r.Handle("/message", sseServer.MessageHandler())
		})
	}

	// Static files
	if cfg != nil && cfg.DevMode {
		// Dev mode: serve from disk for live editing (no cache)
		router.Handle("/static/*", http.StripPrefix("/static/",
			http.FileServer(http.Dir("internal/web/static"))))
	} else {
		staticSub, _ := fs.Sub(staticFS, "static")
		router.Handle("/static/*", http.StripPrefix("/static/",
			StaticCacheHeaders(http.FileServer(http.FS(staticSub)))))
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
		r.Get("/", srv.handleDashboardPage)
		r.Get("/logs", srv.handleLogsPage)
		r.Get("/sources", srv.handleSourcesPage)
		r.Get("/watches", srv.handleWatchesPage)
		r.Get("/alerts", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/watches", http.StatusMovedPermanently)
		})
		r.Get("/tools", srv.handleToolsPage)
		r.Get("/profile", srv.handleProfilePage)
	})

	// Settings (admin) — consolidated page for settings, users, setup, profile
	router.Group(func(r chi.Router) {
		r.Use(srv.RedirectToOnboardingIfNeeded)
		r.Use(RequireAdmin)
		r.Get("/settings", srv.handleSettingsPage)
		// Legacy redirects
		r.Get("/admin/users", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/settings", http.StatusMovedPermanently)
		})
		r.Get("/admin/settings", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/settings", http.StatusMovedPermanently)
		})
	})

	// Debug/pprof endpoints — admin only
	router.Group(func(r chi.Router) {
		r.Use(srv.RedirectToOnboardingIfNeeded)
		r.Use(RequireAdmin)
		r.HandleFunc("/debug/pprof/", pprof.Index)
		r.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		r.HandleFunc("/debug/pprof/profile", pprof.Profile)
		r.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		r.HandleFunc("/debug/pprof/trace", pprof.Trace)
		r.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
		r.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		r.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
		r.Handle("/debug/pprof/block", pprof.Handler("block"))
		r.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
		r.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	})

	// API
	router.Route("/api", func(r chi.Router) {
		r.Use(DecompressRequest(10 << 20)) // 10MB decompressed limit (zip bomb protection)

		// CORS for cross-origin browser requests (JS error tracking)
		r.Use(srv.DynamicCORSMiddleware)

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
			r.Get("/services", srv.handleListServices)
			r.Get("/event_types", srv.handleListEventTypes)
			r.Get("/connectors", srv.handleListConnectors)
			r.Get("/connectors/{id}", srv.handleGetConnectorAPI)
			r.Get("/overview", srv.handleOverviewAPI)
			r.Get("/tools", srv.handleToolsAPI)
			r.Get("/logs/poll", srv.handleLogsPoll)
			r.Get("/logs/{id}", srv.handleGetLogDetail)
			// Watches (agent-first)
			if srv.watchStore != nil {
				r.Get("/watches", srv.handleListWatches)
				r.Get("/watches/{id}", srv.handleGetWatch)
				r.Get("/watches/{id}/runs", srv.handleListWatchRuns)
				r.Get("/watches/{id}/alerts", srv.handleListWatchAlerts)
				r.Get("/watches/alerts/{alertId}", srv.handleGetWatchAlert)
				r.Get("/watches/alerts/count", srv.handleWatchAlertCount)
			}

			// Error groups
			if srv.errorGroupStore != nil {
				r.Get("/errors", srv.handleListErrorGroups)
				r.Get("/errors/{fingerprint}", srv.handleGetErrorGroup)
			}

			// Health checks
			if srv.healthCheckStore != nil {
				r.Get("/healthchecks", srv.handleListHealthChecks)
				r.Get("/healthchecks/{id}/results", srv.handleHealthCheckResults)
				r.Get("/healthchecks/uptime", srv.handleUptimeSummary)
			}

			// Trends + Analytics
			if srv.trendStore != nil {
				r.Get("/trends", srv.handleTrendsAPI)
				r.Get("/trends/deploys", srv.handleDeployMarkersAPI)
			}
			if srv.analyticsStore != nil {
				r.Get("/analytics/summary", srv.handleAnalyticsSummaryAPI)
				r.Get("/analytics/endpoints", srv.handleTopEndpointsAPI)
				r.Get("/analytics/heatmap", srv.handleTrafficHeatmapAPI)
			}

			// User Journey + Session Timeline
			if srv.journeyStore != nil {
				r.Get("/journeys/sessions", srv.handleListSessionsAPI)
				r.Get("/journeys/sessions/{sessionID}", srv.handleGetSessionAPI)
				r.Get("/journeys/sessions/{sessionID}/requests", srv.handleSessionRequestsAPI)
				r.Get("/journeys/sessions/{sessionID}/timeline", srv.handleSessionTimelineAPI)
				r.Get("/journeys/user/{userID}", srv.handleUserJourneyAPI)
				r.Get("/journeys/paths", srv.handlePathAnalysisAPI)
				r.Get("/journeys/funnels", srv.handleListFunnelsAPI)
				r.Get("/journeys/funnels/{funnelID}", srv.handleAnalyzeFunnelAPI)
				r.Get("/journeys/timeline/{logID}", srv.handleRequestTimelineAPI)
			}

			// MCP activity
			if srv.mcpActivityStore != nil {
				r.Get("/mcp/activity/stats", srv.handleMCPActivityStats)
				r.Get("/mcp/activity", srv.handleMCPActivity)
			}

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
			r.Put("/connectors/{id}", srv.handleUpdateConnectorAPI)
			r.Post("/connectors/{id}/test", srv.handleTestConnectorAPI)
			r.Delete("/connectors/{id}", srv.handleDeleteConnectorAPI)
			// Watches (agent-first) — write
			if srv.watchStore != nil {
				r.Post("/watches", srv.handleCreateWatch)
				r.Delete("/watches/{id}", srv.handleDeleteWatch)
				r.Post("/watches/alerts/{alertId}/dismiss", srv.handleDismissWatchAlert)
				r.Post("/watches/alerts/{alertId}/acknowledge", srv.handleAcknowledgeWatchAlert)
			}

			// Error groups (write)
			if srv.errorGroupStore != nil {
				r.Post("/errors/{fingerprint}/resolve", srv.handleResolveErrorGroup)
				r.Post("/errors/{fingerprint}/ignore", srv.handleIgnoreErrorGroup)
			}

			// Funnels (write)
			if srv.journeyStore != nil {
				r.Post("/journeys/funnels", srv.handleCreateFunnelAPI)
				r.Delete("/journeys/funnels/{funnelID}", srv.handleDeleteFunnelAPI)
			}

			// Health checks (write)
			if srv.healthCheckStore != nil {
				r.Post("/healthchecks", srv.handleCreateHealthCheck)
				r.Delete("/healthchecks/{id}", srv.handleDeleteHealthCheck)
			}

			if srv.serverStore != nil && srv.metricStore != nil {
				r.Put("/servers/{id}", srv.handleUpdateServer)
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
			r.Get("/settings/cors", srv.handleGetCORSOrigins)
			r.Put("/settings/cors", srv.handleUpdateCORSOrigins)
			if srv.auditStore != nil {
				r.Get("/audit-log", srv.handleAuditLog)
			}
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
			r.Get("/profile/mcp-token", srv.handleGetOwnMCPToken)
		})

		// Dev-mode live-reload endpoint
		if cfg != nil && cfg.DevMode {
			r.Get("/dev/hash", srv.handleDevHash)
		}
	})

	srv.Router = router
	return srv
}

// Shutdown gracefully shuts down SSE connections and other resources.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.loginLimiter != nil {
		s.loginLimiter.Stop()
	}
	if s.apiLimiter != nil {
		s.apiLimiter.Stop()
	}
	if s.sseServer != nil {
		return s.sseServer.Shutdown(ctx)
	}
	return nil
}

// getEffectiveCORSOrigins returns the CORS allowed origins from the env var (if set) or from the DB.
// Returns a slice of origin strings parsed from the comma-separated value.
func (s *Server) getEffectiveCORSOrigins(ctx context.Context) []string {
	if s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0 {
		return s.cfg.CORSAllowedOrigins
	}
	if s.settingsStore != nil {
		raw, err := s.settingsStore.GetCORSOrigins(ctx)
		if err == nil && raw != "" {
			return parseCORSOriginsString(raw)
		}
	}
	return nil
}

// parseCORSOriginsString splits a comma-separated origins string into a slice,
// trimming whitespace from each entry and filtering empty strings.
func parseCORSOriginsString(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
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

// audit logs an admin action asynchronously. Safe to call even if auditStore is nil.
func (s *Server) audit(r *http.Request, action, targetType, targetID, details string) {
	if s.auditStore == nil {
		return
	}
	user := UserFromContext(r.Context())
	if user == nil {
		return
	}
	// Fire-and-forget to avoid slowing down the request
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.auditStore.Log(ctx, store.LogAuditParams{
			UserID:     user.ID,
			UserEmail:  user.Email,
			Action:     action,
			TargetType: targetType,
			TargetID:   targetID,
			Details:    details,
			IPAddress:  r.RemoteAddr,
		})
	}()
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	entries, err := s.auditStore.Recent(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch audit log")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{
		"status":  "ok",
		"version": version.Version,
	}

	// Check database connectivity
	if s.db != nil {
		if err := s.db.PingContext(r.Context()); err != nil {
			checks["database"] = "error"
			checks["status"] = "degraded"
		} else {
			checks["database"] = "ok"
		}
	}

	status := http.StatusOK
	if checks["status"] != "ok" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, checks)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":                version.Version,
		"commit":                 version.Commit,
		"date":                   version.Date,
		"is_docker":              isDocker(),
		"api_version":            APIVersion,
		"min_client_api_version": MinClientAPIVersion,
		"capabilities":           serverCapabilities,
	})
}

func (s *Server) handleVersionCheck(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("force") == "1" {
		s.versionChecker.invalidateCache()
	}
	resp, err := s.versionChecker.check()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"current_version":  version.Version,
			"update_available": false,
			"error":            err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleVersionBanner(w http.ResponseWriter, r *http.Request) {
	resp, err := s.versionChecker.check()
	if err != nil || !resp.UpdateAvailable {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="update-banner" id="update-banner">
  <span>OpenTrace <strong>v%s</strong> is available (you have v%s).</span>
  <a href="%s" target="_blank" rel="noopener">View release notes</a>
  <button onclick="this.closest('.update-banner').remove()" class="update-banner-dismiss" aria-label="Dismiss">&times;</button>
</div>`, resp.LatestVersion, resp.CurrentVersion, resp.ReleaseURL)
}

// isDocker returns true when running inside a Docker container.
func isDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
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
