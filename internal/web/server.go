package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	assetsFS "github.com/adham90/opentrace/assets"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/server"
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

// ReliabilityProvider returns recent reliability data for health checks.
// Implemented by healthcheck.Scheduler.
type ReliabilityProvider interface {
	Reliability(checkID string) float64
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
	errorImpactStore   store.ErrorImpactStore
	traceStore         store.TraceStore
	investigationSessionStore store.InvestigationSessionStore
	codeEntityStore          store.CodeEntityStore
	deployStore              store.DeployStore
	eventStore               store.EventStore
	testCorrelationStore     store.TestCorrelationStore
	reliabilityProvider      ReliabilityProvider
	versionChecker           *versionChecker
	sseServer      *mcpgoserver.SSEServer
	loginLimiter   *RateLimiter
	apiLimiter     *RateLimiter
	secureCookies  bool
	logsConnMu     sync.Mutex
	metricsConnMu  sync.Mutex

	// Domain modules (isolated packages mounted on the API router)
	sharedDeps *server.Deps
	modules    []server.Module

	// Async log ingestion queue (nil = synchronous fallback)
	ingestQueue *IngestQueue

	// Bounded audit log channel with background worker
	auditCh  chan auditEntry
	auditWg  sync.WaitGroup
	auditCtx context.Context // parent context for audit write timeouts
}

// auditEntry is an enqueued audit log event.
type auditEntry struct {
	userID     string
	userEmail  string
	action     string
	targetType string
	targetID   string
	details    string
	ipAddress  string
}

// ServerDeps holds all dependencies for the web server.
type ServerDeps struct {
	Ctx           context.Context // app lifecycle context; nil defaults to Background
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
	ErrorImpactStore     store.ErrorImpactStore
	TraceStore                   store.TraceStore
	InvestigationSessionStore    store.InvestigationSessionStore
	CodeEntityStore              store.CodeEntityStore
	DeployStore                  store.DeployStore
	EventStore                   store.EventStore
	TestCorrelationStore         store.TestCorrelationStore
	IngestQueue                  *IngestQueue
	ReliabilityProvider          ReliabilityProvider
	SharedDeps                   *server.Deps
	Modules                      []server.Module
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
		errorImpactStore:   deps.ErrorImpactStore,
		traceStore:                deps.TraceStore,
		investigationSessionStore: deps.InvestigationSessionStore,
		codeEntityStore:           deps.CodeEntityStore,
		deployStore:               deps.DeployStore,
		eventStore:                deps.EventStore,
		testCorrelationStore:      deps.TestCorrelationStore,
		ingestQueue:               deps.IngestQueue,
		reliabilityProvider:       deps.ReliabilityProvider,
		sharedDeps:                deps.SharedDeps,
		modules:                   deps.Modules,
		versionChecker:      newVersionChecker("adham90", "opentrace"),
		auditCh:            make(chan auditEntry, 256),
	}

	// Set audit context — used as parent for per-write timeouts
	srv.auditCtx = deps.Ctx
	if srv.auditCtx == nil {
		srv.auditCtx = context.Background()
	}

	// Start audit log worker
	srv.auditWg.Add(1)
	go srv.auditWorker()

	cfg := deps.Cfg

	var trustedProxies []string
	if cfg != nil {
		trustedProxies = cfg.TrustedProxies
	}
	srv.loginLimiter = NewRateLimiter(10, 1*time.Minute, trustedProxies)
	srv.apiLimiter = NewRateLimiter(120, 1*time.Minute, trustedProxies)
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
	router.Use(PrometheusMiddleware)
	router.Use(SecurityHeaders)
	router.Use(wrapCompressSkipMCP(middleware.Compress(5))) // gzip compression, bypassed for /mcp/ SSE
	router.Use(MaxBodySize(10 << 20))  // 10 MB global body limit
	router.Use(srv.SessionAuth)        // skips /static/ and /healthz paths internally
	router.Use(CSRFProtect)            // double-submit cookie CSRF protection

	router.Get("/healthz", srv.handleHealthCheck)
	router.Get("/api/version", srv.handleVersion)
	router.Get("/api/version/check", srv.handleVersionCheck)
	router.Get("/api/version/banner", srv.handleVersionBanner)
	router.Get("/api/docs", srv.handleSwaggerUI)
	router.Get("/api/docs/openapi.yaml", srv.handleOpenAPISpec)

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

	// Static files (legacy /static/ route)
	if cfg != nil && cfg.DevMode {
		router.Handle("/static/*", http.StripPrefix("/static/",
			http.FileServer(http.Dir("internal/web/static"))))
	} else {
		staticSub, _ := fs.Sub(staticFS, "static")
		router.Handle("/static/*", http.StripPrefix("/static/",
			StaticCacheHeaders(http.FileServer(http.FS(staticSub)))))
	}

	// Assets (templ + Tailwind CSS + JS)
	if cfg != nil && cfg.DevMode {
		router.Handle("/assets/*", http.StripPrefix("/assets/",
			http.FileServer(http.Dir("assets"))))
	} else {
		router.Handle("/assets/*", http.StripPrefix("/assets/",
			StaticCacheHeaders(http.FileServer(http.FS(assetsFS.Files)))))
	}

	// Expose the root router and login limiter so auth/onboarding modules
	// can register unauthenticated routes (login, register, logout, onboarding).
	if srv.sharedDeps != nil {
		srv.sharedDeps.RootRouter = router
		srv.sharedDeps.LoginLimiter = loginLimiter.Middleware
		srv.sharedDeps.SecureCookies = srv.secureCookies
	}

	// Pages — require auth, redirect to onboarding if no users.
	// The page router reference is exposed via sharedDeps.PageRouter so
	// domain modules (e.g. dashboard) can register page routes.
	router.Group(func(r chi.Router) {
		r.Use(srv.RedirectToOnboardingIfNeeded)
		// Page routes for logs, errors, health, connectors, tools, sessions,
		// profile, and watchers are handled by domain modules via PageRouter.
		if srv.sharedDeps != nil {
			srv.sharedDeps.PageRouter = r
		}
	})

	// Settings (admin) — settings, setup, and users page routes are handled
	// by the settings module via AdminPageRouter.
	router.Group(func(r chi.Router) {
		r.Use(srv.RedirectToOnboardingIfNeeded)
		r.Use(RequireAdmin)
		// Legacy redirects
		r.Get("/admin/users", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/users", http.StatusMovedPermanently)
		})
		r.Get("/admin/settings", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/settings", http.StatusMovedPermanently)
		})
		// Expose admin page router for modules
		if srv.sharedDeps != nil {
			srv.sharedDeps.AdminPageRouter = r
		}
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
		r.Handle("/debug/metrics", promhttp.Handler())
	})

	// API
	router.Route("/api", func(r chi.Router) {
		r.Use(DecompressRequest(10 << 20)) // 10MB decompressed limit (zip bomb protection)

		// CORS for cross-origin browser requests (JS error tracking)
		r.Use(srv.DynamicCORSMiddleware)

		// Log ingestion with dynamic API key auth + rate limiting
		r.With(apiLimiter.Middleware, srv.DynamicAPIKeyAuth).Post("/logs", srv.handleIngestLogs)

		// Expose the API router and middleware so domain modules can
		// register webhook routes with API key auth (see deploys, events,
		// servers modules).
		if srv.sharedDeps != nil {
			srv.sharedDeps.APIRouter = r
			srv.sharedDeps.APIKeyAuth = srv.DynamicAPIKeyAuth
			srv.sharedDeps.APIRateLimiter = apiLimiter.Middleware
		}

		// Read/Write API routes for logs, connectors, tools, errors,
		// healthchecks, trends, analytics, error impact, journeys, traces,
		// deploys, events, code entities, test gaps, mcp activity,
		// investigations, servers, watches, and settings are handled
		// by domain modules. See internal/modules/ and cmd/opentrace/modules.go.

		// Dev-mode live-reload endpoint
		if cfg != nil && cfg.DevMode {
			r.Get("/dev/hash", srv.handleDevHash)
		}

		// Mount domain modules (isolated packages)
		if srv.sharedDeps != nil {
			r.Group(func(r chi.Router) {
				r.Use(srv.requireAuthOrOnboardingAPI)
				for _, m := range srv.modules {
					m.Mount(r, srv.sharedDeps)
				}
			})
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

	// Flush and stop the ingest queue before closing other resources
	if s.ingestQueue != nil {
		s.ingestQueue.Stop()
	}

	// Drain audit log channel
	if s.auditCh != nil {
		close(s.auditCh)
		s.auditWg.Wait()
	}

	if s.sseServer != nil {
		return s.sseServer.Shutdown(ctx)
	}
	return nil
}

// getEffectiveCORSOrigins returns the CORS allowed origins from the env var (if set) or from the DB.
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

func (s *Server) audit(r *http.Request, action, targetType, targetID, details string) {
	if s.auditStore == nil {
		return
	}
	user := UserFromContext(r.Context())
	if user == nil {
		return
	}
	select {
	case s.auditCh <- auditEntry{
		userID:     user.ID,
		userEmail:  user.Email,
		action:     action,
		targetType: targetType,
		targetID:   targetID,
		details:    details,
		ipAddress:  r.RemoteAddr,
	}:
	default:
		slog.Warn("audit entry dropped (channel full)", "action", action)
	}
}

func (s *Server) auditWorker() {
	defer s.auditWg.Done()
	for entry := range s.auditCh {
		ctx, cancel := context.WithTimeout(s.auditCtx, 5*time.Second)
		if err := s.auditStore.Log(ctx, store.LogAuditParams{
			UserID:     entry.userID,
			UserEmail:  entry.userEmail,
			Action:     entry.action,
			TargetType: entry.targetType,
			TargetID:   entry.targetID,
			Details:    entry.details,
			IPAddress:  entry.ipAddress,
		}); err != nil {
			slog.Warn("audit log write failed", "action", entry.action, "error", err)
		}
		cancel()
	}
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{
		"status":  "ok",
		"version": version.Version,
	}
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

func isDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

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
