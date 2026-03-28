package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	mcpserver "github.com/adham90/opentrace/internal/mcp"
	srvpkg "github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/mark3labs/mcp-go/server"
)

// wrapCompressSkipMCP wraps chi's Compress middleware so that /mcp/ paths
// bypass it entirely. Simply stripping Accept-Encoding is not enough because
// chi's compressResponseWriter still wraps the http.ResponseWriter, which
// breaks http.Flusher — causing SSE events to be buffered until close.
func wrapCompressSkipMCP(compressMw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		compressed := compressMw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/mcp/") {
				next.ServeHTTP(w, r)
				return
			}
			compressed.ServeHTTP(w, r)
		})
	}
}

// MCPTokenAuth is middleware that authenticates requests using a Bearer token
// validated against the user's MCP token in the database. It sets the
// authenticated user in the request context.
func (s *Server) MCPTokenAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.userStore == nil {
			http.Error(w, "authentication not configured", http.StatusInternalServerError)
			return
		}

		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			time.Sleep(100 * time.Millisecond) // Normalize timing
			http.Error(w, "missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		user, err := s.userStore.GetByMCPToken(ctx, token)
		if err != nil || user == nil {
			time.Sleep(100 * time.Millisecond) // Normalize timing to prevent token enumeration
			http.Error(w, "invalid or disabled MCP token", http.StatusUnauthorized)
			return
		}

		if !user.IsActive {
			http.Error(w, "user account is disabled", http.StatusForbidden)
			return
		}

		// Store user in context for downstream use.
		rctx := srvpkg.WithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(rctx))
	})
}

// setupMCPStreamableHTTP creates the Streamable HTTP server backed by an
// MCPServer with all tools registered. This replaces the deprecated SSE
// transport per MCP spec 2025-06-18.
func (s *Server) setupMCPStreamableHTTP() *server.StreamableHTTPServer {
	deps := mcpserver.Deps{
		Ctx:      s.auditCtx,
		Registry: s.registry,
		Config:   s.cfg,
		Stores: store.Stores{
			DSStore:          s.dsStore,
			LogStore:         s.logStore,
			ServerStore:      s.serverStore,
			MetricStore:      s.metricStore,
			UserStore:        s.userStore,
			SettingsStore:    s.settingsStore,
			MCPActivityStore: s.mcpActivityStore,
			AuditStore:       s.auditStore,
			WatchStore:       s.watchStore,
			ErrorGroupStore:  s.errorGroupStore,
			HealthCheckStore: s.healthCheckStore,
			AgentNoteStore:   s.agentNoteStore,
			TrendStore:       s.trendStore,
			AnalyticsStore:   s.analyticsStore,
			ErrorImpactStore: s.errorImpactStore,
		},
		WatchMetrics: s.watchMetrics,
	}

	mcpSrv := mcpserver.NewConfiguredServer(deps, true, nil)
	s.mcpServer = mcpSrv // store for notification dispatch

	httpServer := server.NewStreamableHTTPServer(mcpSrv,
		server.WithEndpointPath("/mcp"),
		server.WithHeartbeatInterval(30*time.Second),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if user := srvpkg.UserFromContext(r.Context()); user != nil {
				return srvpkg.WithUser(ctx, user)
			}
			return ctx
		}),
	)

	return httpServer
}

// setupMCPSSE creates the legacy SSE server for backward compatibility.
// Deprecated: Use setupMCPStreamableHTTP instead.
func (s *Server) setupMCPSSE() *server.SSEServer {
	deps := mcpserver.Deps{
		Ctx:      s.auditCtx,
		Registry: s.registry,
		Config:   s.cfg,
		Stores: store.Stores{
			DSStore:          s.dsStore,
			LogStore:         s.logStore,
			ServerStore:      s.serverStore,
			MetricStore:      s.metricStore,
			UserStore:        s.userStore,
			SettingsStore:    s.settingsStore,
			MCPActivityStore: s.mcpActivityStore,
			AuditStore:       s.auditStore,
			WatchStore:       s.watchStore,
			ErrorGroupStore:  s.errorGroupStore,
			HealthCheckStore: s.healthCheckStore,
			AgentNoteStore:   s.agentNoteStore,
			TrendStore:       s.trendStore,
			AnalyticsStore:   s.analyticsStore,
			ErrorImpactStore: s.errorImpactStore,
		},
		WatchMetrics: s.watchMetrics,
	}

	mcpSrv := mcpserver.NewConfiguredServer(deps, true, nil)

	sseServer := server.NewSSEServer(mcpSrv,
		server.WithStaticBasePath("/mcp"),
		server.WithUseFullURLForMessageEndpoint(false),
		server.WithKeepAliveInterval(30*time.Second),
		server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if user := srvpkg.UserFromContext(r.Context()); user != nil {
				return srvpkg.WithUser(ctx, user)
			}
			return ctx
		}),
	)

	return sseServer
}

// mcpUserFromContext returns the user set by MCPTokenAuth, for role checks.
func mcpUserFromContext(ctx context.Context) *store.User {
	return srvpkg.UserFromContext(ctx)
}
