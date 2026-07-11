package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/mcp/envscope"
	srvpkg "github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// prepareSSE gets the response ready for a long-lived SSE stream:
//   - Signals reverse proxies (nginx via X-Accel-Buffering, Cloudflare,
//     any proxy honoring no-transform) not to buffer. Caddy ignores these
//     and needs `flush_interval -1` in its reverse_proxy config.
//   - Clears the http.Server WriteTimeout on this connection. Without this,
//     the global WriteTimeout (set in main.go) would kill the SSE stream
//     mid-session, making every tool call after that point hang.
func prepareSSE(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Accel-Buffering", "no")
		h.Set("Cache-Control", "no-cache, no-transform")

		// SSE is a long-lived stream — the global WriteTimeout on http.Server
		// would otherwise tear it down. Zero time = no deadline.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			// Not fatal — if the underlying writer doesn't support deadlines,
			// the handler will still run, just bounded by WriteTimeout.
			slog.Warn("sse: clearing write deadline failed", "error", err)
		}

		next.ServeHTTP(w, r)
	})
}

// wrapCompressSkipMCP wraps chi's Compress middleware so that /mcp/ paths
// bypass it entirely. Simply stripping Accept-Encoding is not enough because
// chi's compressResponseWriter still wraps the http.ResponseWriter, which
// breaks http.Flusher — causing SSE events to be buffered until close.
func wrapCompressSkipMCP(compressMw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		compressed := compressMw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/mcp/") || strings.HasPrefix(r.URL.Path, "/mcp-sse") {
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

		// Store user and derived env scope in context for downstream use.
		// Tools pull scope via envscope.From; the server-side User object
		// is kept around for role checks via mcpUserFromContext.
		rctx := srvpkg.WithUser(r.Context(), user)
		rctx = envscope.With(rctx, envscope.FromUser(user))
		next.ServeHTTP(w, r.WithContext(rctx))
	})
}

// mcpDeps builds the shared MCP dependency set used by both HTTP transports.
func (s *Server) mcpDeps() mcpserver.Deps {
	return mcpserver.Deps{
		Ctx:      s.auditCtx,
		Registry: s.registry,
		Config:   s.cfg,
		DB:       s.db,
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
}

// mcpServerForRole selects the admin or member MCP server based on the
// authenticated user attached by MCPTokenAuth. It fails closed: any request
// without an admin user gets the read-only member server. This is the
// authorization boundary for the HTTP/SSE transports — the previous code
// handed every authenticated user the admin server, letting a member call
// write/admin tools (e.g. self-promote to admin).
func mcpServerForRole(admin, member *mcp.Server) func(*http.Request) *mcp.Server {
	return func(r *http.Request) *mcp.Server {
		if u := mcpUserFromContext(r.Context()); u != nil && u.Role == store.RoleAdmin {
			return admin
		}
		return member
	}
}

// setupMCPStreamableHTTP creates the Streamable HTTP server. It builds one
// admin server and one member (read-only) server and dispatches per request
// according to the caller's role. This replaces the deprecated SSE transport
// per MCP spec 2025-06-18.
func (s *Server) setupMCPStreamableHTTP() *mcp.StreamableHTTPHandler {
	deps := s.mcpDeps()
	adminSrv := mcpserver.NewConfiguredServer(deps, true, nil)
	memberSrv := mcpserver.NewConfiguredServer(deps, false, nil)

	return mcp.NewStreamableHTTPHandler(
		mcpServerForRole(adminSrv, memberSrv),
		&mcp.StreamableHTTPOptions{
			SessionTimeout: 30 * time.Minute,
		},
	)
}

// setupMCPSSE creates the legacy SSE server for backward compatibility.
// Deprecated: Use setupMCPStreamableHTTP instead.
func (s *Server) setupMCPSSE() *mcp.SSEHandler {
	deps := s.mcpDeps()
	adminSrv := mcpserver.NewConfiguredServer(deps, true, nil)
	memberSrv := mcpserver.NewConfiguredServer(deps, false, nil)

	return mcp.NewSSEHandler(
		mcpServerForRole(adminSrv, memberSrv),
		nil,
	)
}

// mcpUserFromContext returns the user set by MCPTokenAuth, for role checks.
func mcpUserFromContext(ctx context.Context) *store.User {
	return srvpkg.UserFromContext(ctx)
}
