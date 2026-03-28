package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/ingest"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/watcher"
)

const (
	// maxRequestBodyBytes is the global limit on HTTP request body size (10 MB).
	maxRequestBodyBytes = 10 << 20
	// auditChannelBuffer is the capacity of the async audit-log channel.
	auditChannelBuffer = 256
)

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
	settingsStore store.SettingsStore
	registry      *connector.Registry
	cfg           *config.Config
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
	errorImpactStore   store.ErrorImpactStore
	traceStore         store.TraceStore
	investigationSessionStore store.InvestigationSessionStore
	codeEntityStore          store.CodeEntityStore
	deployStore              store.DeployStore
	eventStore               store.EventStore
	testCorrelationStore     store.TestCorrelationStore
	reliabilityProvider      ReliabilityProvider
	sseServer         *mcpgoserver.SSEServer
	streamableServer  *mcpgoserver.StreamableHTTPServer
	loginLimiter   *RateLimiter
	apiLimiter     *RateLimiter
	metricsConnMu  sync.Mutex

	// Domain modules (isolated packages mounted on the API router)
	sharedDeps *server.Deps
	modules    []server.Module

	// Log ingestion handler (extracted to internal/ingest)
	ingestHandler *ingest.Handler

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
	Ctx     context.Context // app lifecycle context; nil defaults to Background
	DB      *sql.DB
	Stores  store.Stores

	Registry             *connector.Registry
	Cfg                  *config.Config
	WatchStreamEvaluator *watcher.WatchStreamEvaluator
	WatchMetrics         *watcher.WatchMetrics
	IngestQueue          *ingest.Queue
	ReliabilityProvider  ReliabilityProvider
	SharedDeps           *server.Deps
	Modules              []server.Module
}

// NewServer creates a new Server with the given dependencies and sets up routes.
func NewServer(dsStore store.DataSourceStore, logStore store.LogStore, registry *connector.Registry, cfg *config.Config) *Server {
	return NewServerWithDeps(ServerDeps{
		Stores: store.Stores{
			DSStore:  dsStore,
			LogStore: logStore,
		},
		Registry: registry,
		Cfg:      cfg,
	})
}

// NewServerWithDeps creates a new Server using the ServerDeps struct.
func NewServerWithDeps(deps ServerDeps) *Server {
	srv := &Server{
		db:                        deps.DB,
		dsStore:                   deps.Stores.DSStore,
		logStore:                  deps.Stores.LogStore,
		serverStore:               deps.Stores.ServerStore,
		metricStore:               deps.Stores.MetricStore,
		userStore:                 deps.Stores.UserStore,
		settingsStore:             deps.Stores.SettingsStore,
		registry:                  deps.Registry,
		cfg:                       deps.Cfg,
		mcpActivityStore:          deps.Stores.MCPActivityStore,
		auditStore:                deps.Stores.AuditStore,
		watchStream:               deps.WatchStreamEvaluator,
		watchStore:                deps.Stores.WatchStore,
		watchMetrics:              deps.WatchMetrics,
		errorGroupStore:           deps.Stores.ErrorGroupStore,
		healthCheckStore:          deps.Stores.HealthCheckStore,
		agentNoteStore:            deps.Stores.AgentNoteStore,
		trendStore:                deps.Stores.TrendStore,
		analyticsStore:            deps.Stores.AnalyticsStore,
		errorImpactStore:          deps.Stores.ErrorImpactStore,
		traceStore:                deps.Stores.TraceStore,
		investigationSessionStore: deps.Stores.InvestigationSessionStore,
		codeEntityStore:           deps.Stores.CodeEntityStore,
		deployStore:               deps.Stores.DeployStore,
		eventStore:                deps.Stores.EventStore,
		testCorrelationStore:      deps.Stores.TestCorrelationStore,
		reliabilityProvider:       deps.ReliabilityProvider,
		sharedDeps:                deps.SharedDeps,
		modules:                   deps.Modules,
		auditCh:                   make(chan auditEntry, auditChannelBuffer),
	}

	// Set audit context — used as parent for per-write timeouts
	srv.auditCtx = deps.Ctx
	if srv.auditCtx == nil {
		srv.auditCtx = context.Background()
	}

	// Start audit log worker
	srv.auditWg.Add(1)
	go srv.auditWorker()

	// Create the log ingestion handler
	srv.ingestHandler = &ingest.Handler{
		LogStore:         deps.Stores.LogStore,
		SettingsStore:    deps.Stores.SettingsStore,
		ErrorGroupStore:  deps.Stores.ErrorGroupStore,
		ErrorImpactStore: deps.Stores.ErrorImpactStore,
		CodeEntityStore:  deps.Stores.CodeEntityStore,
		TraceStore:       deps.Stores.TraceStore,
		DSStore:          deps.Stores.DSStore,
		Registry:         deps.Registry,
		Cfg:              deps.Cfg,
		Queue:            deps.IngestQueue,
	}
	if deps.WatchStreamEvaluator != nil {
		srv.ingestHandler.WatchStream = deps.WatchStreamEvaluator
	}

	cfg := deps.Cfg

	var trustedProxies []string
	if cfg != nil {
		trustedProxies = cfg.TrustedProxies
	}
	srv.loginLimiter = NewRateLimiter(10, 1*time.Minute, trustedProxies)
	srv.apiLimiter = NewRateLimiter(120, 1*time.Minute, trustedProxies)
	loginLimiter := srv.loginLimiter
	apiLimiter := srv.apiLimiter

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)
	router.Use(PrometheusMiddleware)
	router.Use(wrapCompressSkipMCP(middleware.Compress(5))) // gzip compression, bypassed for /mcp/ SSE
	router.Use(MaxBodySize(maxRequestBodyBytes)) // 10 MB global body limit
	router.Use(srv.ProxyAuth)          // trusted proxy headers (cloud managed mode)

	router.Get("/healthz", srv.handleHealthCheck)
	router.Get("/readyz", srv.handleReadiness)

	// MCP transport — authenticated via Bearer token (MCP token).
	// Supports both Streamable HTTP (recommended, /mcp) and legacy SSE (/mcp/sse).
	if srv.userStore != nil && srv.registry != nil {
		// Streamable HTTP transport (MCP spec 2025-06-18+)
		streamableServer := srv.setupMCPStreamableHTTP()
		srv.streamableServer = streamableServer
		router.Route("/mcp", func(r chi.Router) {
			r.Use(apiLimiter.Middleware)
			r.Use(srv.MCPTokenAuth)
			// Streamable HTTP handles POST/GET/DELETE on the endpoint path.
			r.Handle("/*", streamableServer)
		})

		// Legacy SSE transport (deprecated, kept for backward compatibility)
		sseServer := srv.setupMCPSSE()
		srv.sseServer = sseServer
		router.Route("/mcp-sse", func(r chi.Router) {
			r.Use(apiLimiter.Middleware)
			r.Use(srv.MCPTokenAuth)
			r.Handle("/sse", sseServer.SSEHandler())
			r.Handle("/message", sseServer.MessageHandler())
		})
	}

	// Static files removed — headless API-only server.

	// Expose the root router and login limiter so auth/onboarding modules
	// can register unauthenticated routes (login, register, logout, onboarding).
	if srv.sharedDeps != nil {
		srv.sharedDeps.RootRouter = router
		srv.sharedDeps.LoginLimiter = loginLimiter.Middleware
	}

	// Debug/pprof endpoints — admin only
	router.Group(func(r chi.Router) {
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
		r.Use(DecompressRequest(maxRequestBodyBytes)) // 10MB decompressed limit (zip bomb protection)

		// CORS for cross-origin browser requests (JS error tracking)
		r.Use(srv.DynamicCORSMiddleware)

		// Log ingestion with dynamic API key auth + rate limiting
		r.With(apiLimiter.Middleware, srv.DynamicAPIKeyAuth).Post("/logs", srv.ingestHandler.HandleIngestLogs)

		// Expose the API router and middleware so domain modules can
		// register webhook routes with API key auth (see deploys, events,
		// servers modules).
		if srv.sharedDeps != nil {
			srv.sharedDeps.APIRouter = r
			srv.sharedDeps.APIKeyAuth = srv.DynamicAPIKeyAuth
			srv.sharedDeps.APIRateLimiter = apiLimiter.Middleware
		}

		// Mount domain modules (isolated packages).
		// Remaining modules only register webhook/ingestion routes on
		// deps.APIRouter (this router) with APIKeyAuth, so no session
		// auth middleware is needed here.
		if srv.sharedDeps != nil {
			for _, m := range srv.modules {
				m.Mount(r, srv.sharedDeps)
			}
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
	if s.ingestHandler != nil && s.ingestHandler.Queue != nil {
		s.ingestHandler.Queue.Stop()
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

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": "no database"})
		return
	}
	if err := s.db.PingContext(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": "database unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// Version check and banner handlers removed — headless API-only server.

