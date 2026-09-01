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
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/watcher"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxRequestBodyBytes is the global limit on HTTP request body size (10 MB).
	maxRequestBodyBytes = 10 << 20
	// auditChannelBuffer is the capacity of the async audit-log channel.
	auditChannelBuffer = 256
	// auditWriteTimeout bounds a single audit-log store write.
	auditWriteTimeout = 5 * time.Second
)

// ReliabilityProvider returns recent reliability data for health checks.
// Implemented by healthcheck.Scheduler.
type ReliabilityProvider interface {
	Reliability(checkID string) float64
}

// Server holds the HTTP server and its dependencies.
type Server struct {
	Router              chi.Router
	db                  *sql.DB
	dsStore             store.DataSourceStore
	logStore            store.LogStore
	serverStore         store.ServerStore
	metricStore         store.MetricStore
	userStore           store.UserStore
	settingsStore       store.SettingsStore
	registry            *connector.Registry
	cfg                 *config.Config
	mcpActivityStore    store.MCPActivityStore
	auditStore          store.AuditStore
	watchStream         *watcher.WatchStreamEvaluator
	watchStore          store.WatchStore
	watchMetrics        *watcher.WatchMetrics
	oncallStatus        func() (time.Time, string, int)
	errorGroupStore     store.ErrorGroupStore
	healthCheckStore    store.HealthCheckStore
	agentNoteStore      store.AgentNoteStore
	errorImpactStore    store.ErrorImpactStore
	traceStore          store.TraceStore
	codeEntityStore     store.CodeEntityStore
	deployStore         store.DeployStore
	reliabilityProvider ReliabilityProvider
	sseServer           *mcp.SSEHandler
	sseAPILimiter       *RateLimiter
	streamableServer    *mcp.StreamableHTTPHandler
	loginLimiter        *RateLimiter
	apiLimiter          *RateLimiter
	ingestLimiter       *RateLimiter
	// Handler is the top-level http.Handler (wraps Router with SSE mux)
	Handler http.Handler
	// sseGate is the per-session initialize gate for the SSE transport.
	// Held on the Server so Shutdown can stop its sweep goroutine.
	sseGate *sseInitGate

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
	Ctx    context.Context // app lifecycle context; nil defaults to Background
	DB     *sql.DB
	Stores store.Stores

	Registry             *connector.Registry
	Cfg                  *config.Config
	WatchStreamEvaluator *watcher.WatchStreamEvaluator
	WatchMetrics         *watcher.WatchMetrics
	IngestQueue          *ingest.Queue
	AutoWatcher          *watcher.AutoWatcher
	ReliabilityProvider  ReliabilityProvider
	SharedDeps           *server.Deps
	Modules              []server.Module

	// OnCallStatus reports the on-call agent's health to overview.status.
	// Nil when the agent is not configured.
	OnCallStatus func() (lastSuccess time.Time, lastError string, runsToday int)
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
		db:                  deps.DB,
		dsStore:             deps.Stores.DSStore,
		logStore:            deps.Stores.LogStore,
		serverStore:         deps.Stores.ServerStore,
		metricStore:         deps.Stores.MetricStore,
		userStore:           deps.Stores.UserStore,
		settingsStore:       deps.Stores.SettingsStore,
		registry:            deps.Registry,
		cfg:                 deps.Cfg,
		mcpActivityStore:    deps.Stores.MCPActivityStore,
		auditStore:          deps.Stores.AuditStore,
		watchStream:         deps.WatchStreamEvaluator,
		watchStore:          deps.Stores.WatchStore,
		watchMetrics:        deps.WatchMetrics,
		oncallStatus:        deps.OnCallStatus,
		errorGroupStore:     deps.Stores.ErrorGroupStore,
		healthCheckStore:    deps.Stores.HealthCheckStore,
		agentNoteStore:      deps.Stores.AgentNoteStore,
		errorImpactStore:    deps.Stores.ErrorImpactStore,
		traceStore:          deps.Stores.TraceStore,
		codeEntityStore:     deps.Stores.CodeEntityStore,
		deployStore:         deps.Stores.DeployStore,
		reliabilityProvider: deps.ReliabilityProvider,
		sharedDeps:          deps.SharedDeps,
		modules:             deps.Modules,
		auditCh:             make(chan auditEntry, auditChannelBuffer),
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
		DeployStore:      deps.Stores.DeployStore,
		DSStore:          deps.Stores.DSStore,
		Registry:         deps.Registry,
		Cfg:              deps.Cfg,
		Queue:            deps.IngestQueue,
	}
	cfg := deps.Cfg
	performance := cfg.EffectivePerformance()
	srv.ingestHandler.StartPostProcessor(performance.PostprocessWorkers, performance.PostprocessQueue)
	if deps.WatchStreamEvaluator != nil {
		srv.ingestHandler.WatchStream = deps.WatchStreamEvaluator
	}
	if deps.AutoWatcher != nil {
		srv.ingestHandler.AutoWatch = deps.AutoWatcher
	}

	var trustedProxies []string
	if cfg != nil {
		trustedProxies = cfg.TrustedProxies
	}
	srv.loginLimiter = NewRateLimiter(10, 1*time.Minute, trustedProxies)
	srv.apiLimiter = NewRateLimiter(120, 1*time.Minute, trustedProxies)
	srv.ingestLimiter = NewRateLimiter(performance.IngestRatePerMinute, time.Minute, trustedProxies)
	loginLimiter := srv.loginLimiter
	apiLimiter := srv.apiLimiter
	ingestLimiter := srv.ingestLimiter
	inFlightIngest := InFlightBodyLimit(int64(performance.IngestInFlightMB)<<20, maxRequestBodyBytes)

	router := chi.NewRouter()
	router.Use(conditionalRequestLogger(performance.AccessLog))
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)
	router.Use(PrometheusMiddleware)
	router.Use(wrapCompressSkipMCP(middleware.Compress(5))) // gzip compression, bypassed for /mcp/
	router.Use(MaxBodySize(maxRequestBodyBytes))            // 10 MB global body limit
	router.Use(srv.ProxyAuth)                               // trusted proxy headers (cloud managed mode)

	// The ingest wire contract, public and unauthenticated: it is what a client
	// author (usually a coding agent) reads before there is a key to read it
	// with, and it discloses nothing but the format.
	router.Get("/spec", ingest.HandleSpec)

	router.Get("/healthz", srv.handleHealthCheck)
	router.Get("/readyz", srv.handleReadiness)

	// MCP transport — authenticated via Bearer token (MCP token).
	if srv.userStore != nil && srv.registry != nil {
		// Streamable HTTP transport (MCP spec 2025-06-18+)
		streamableServer := srv.setupMCPStreamableHTTP()
		srv.streamableServer = streamableServer
		router.Route("/mcp", func(r chi.Router) {
			r.Use(apiLimiter.Middleware)
			r.Use(srv.MCPTokenAuth)
			// Bind the session to the authenticated user so the SDK rejects a
			// request that presents someone else's Mcp-Session-Id.
			r.Use(mcpTokenInfoBridge)
			// The streamable transport keeps a hanging GET open for
			// server->client messages and can stream a POST response for
			// minutes; the global WriteTimeout would tear both down. Same
			// reason prepareSSE exists for the legacy /mcp-sse path.
			r.Handle("/*", prepareSSE(streamableServer))
		})

		// SSE transport — stored on srv, mounted via top-level mux
		// (not on the chi router) to avoid Logger/Compress middleware
		// wrapping http.ResponseWriter which breaks http.Flusher.
		srv.sseServer = srv.setupMCPSSE()
		srv.sseAPILimiter = apiLimiter
	}

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
		r.With(ingestLimiter.Middleware, srv.DynamicAPIKeyAuth, inFlightIngest).Post("/logs", srv.ingestHandler.HandleIngestLogs)
		// Flat SDK format (Node/JS SDKs). Shares the same pipeline as /api/logs.
		r.With(ingestLimiter.Middleware, srv.DynamicAPIKeyAuth, inFlightIngest).Post("/v2/logs", srv.ingestHandler.HandleFlatIngest)

		// Expose the API router and middleware so domain modules can
		// register webhook routes with API key auth (see servers module).
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

	// Build top-level handler: if SSE is configured, use an http.ServeMux
	// that routes /mcp-sse to a clean handler (no Logger/Compress wrapping)
	// and everything else to the chi router.
	if srv.sseServer != nil {
		mux := http.NewServeMux()
		gate := newSSEInitGate()
		srv.sseGate = gate
		// Chain: rate-limit -> token auth -> init-order gate -> SSE setup -> SDK handler
		sseHandler := gate.wrap(prepareSSE(srv.sseServer))
		sseHandler = srv.MCPTokenAuth(sseHandler)
		if srv.sseAPILimiter != nil {
			sseHandler = srv.sseAPILimiter.Middleware(sseHandler)
		}
		mux.Handle("/mcp-sse/", http.StripPrefix("/mcp-sse", sseHandler))
		mux.Handle("/mcp-sse", sseHandler) // exact match (no trailing slash)
		mux.Handle("/", router)
		srv.Handler = mux
	} else {
		srv.Handler = router
	}

	return srv
}

// conditionalRequestLogger keeps structured access visibility when explicitly
// enabled, but never synchronously logs the two high-volume ingest routes.
func conditionalRequestLogger(enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		logged := middleware.Logger(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/logs" || r.URL.Path == "/api/v2/logs" {
				next.ServeHTTP(w, r)
				return
			}
			logged.ServeHTTP(w, r)
		})
	}
}

// IngestHandler returns the log ingestion handler so it can be reused by
// non-HTTP transports (e.g. Unix socket listener).
func (s *Server) IngestHandler() *ingest.Handler {
	return s.ingestHandler
}

// Shutdown gracefully shuts down SSE connections and other resources.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.loginLimiter != nil {
		s.loginLimiter.Stop()
	}
	if s.apiLimiter != nil {
		s.apiLimiter.Stop()
	}
	if s.ingestLimiter != nil {
		s.ingestLimiter.Stop()
	}
	if s.sseGate != nil {
		s.sseGate.Stop()
	}

	// Flush and stop the ingest queue before closing other resources
	if s.ingestHandler != nil && s.ingestHandler.Queue != nil {
		s.ingestHandler.Queue.Stop()
	}
	if s.ingestHandler != nil {
		if err := s.ingestHandler.StopPostProcessor(ctx); err != nil {
			return err
		}
	}

	// Drain audit log channel
	if s.auditCh != nil {
		close(s.auditCh)
		s.auditWg.Wait()
	}

	// The new go-sdk SSEHandler has no Shutdown method; connections close when
	// the HTTP server shuts down.
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

// getEffectiveAPIKey resolves the ingestion API key: the env-configured key
// wins, otherwise the DB-stored key. An empty key with a nil error means auth
// is genuinely disabled (no key configured anywhere). A non-nil error means we
// could not find out — callers MUST fail closed rather than treat it as
// "disabled": the store returns ("", nil) for "no row" and a real error only
// for real failures, so an error here is never a configuration statement.
func (s *Server) getEffectiveAPIKey(ctx context.Context) (string, error) {
	if s.cfg != nil && s.cfg.APIKey != "" {
		return s.cfg.APIKey, nil
	}
	if s.settingsStore != nil {
		key, err := s.settingsStore.GetAPIKey(ctx)
		if err != nil {
			return "", err
		}
		return key, nil
	}
	return "", nil
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
	// Writes are detached from the app lifecycle context: main cancels it
	// before Shutdown closes auditCh, so deriving from it directly would make
	// every queued entry fail with "context canceled" — the drain exists
	// precisely to persist those. Values (if any) still propagate; only the
	// cancellation does not. Each write stays bounded by its own timeout.
	base := context.WithoutCancel(s.auditCtx)
	for entry := range s.auditCh {
		ctx, cancel := context.WithTimeout(base, auditWriteTimeout)
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
