package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/store"
	"github.com/mark3labs/mcp-go/server"
)

// skipCompressForSSE strips Accept-Encoding for /mcp/ paths so the
// downstream Compress middleware leaves the SSE stream uncompressed.
// SSE requires raw, unflushed writes; gzip wrapping breaks event delivery.
func skipCompressForSSE(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/mcp/") {
			r.Header.Del("Accept-Encoding")
		}
		next.ServeHTTP(w, r)
	})
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
		rctx := context.WithValue(r.Context(), ctxKeyUser, user)
		next.ServeHTTP(w, r.WithContext(rctx))
	})
}

// setupMCPSSE creates the SSE server backed by an MCPServer with all tools
// registered. Returns the SSE server for route mounting.
func (s *Server) setupMCPSSE() *server.SSEServer {
	// Build MCP deps from the web server's stores.
	deps := mcpserver.Deps{
		Registry:         s.registry,
		WatcherStore:     s.watcherStore,
		AlertStore:       s.alertStore,
		ServerStore:      s.serverStore,
		MetricStore:      s.metricStore,
		UserStore:        s.userStore,
		WatcherRunStore:  s.runStore,
		LogStore:         s.logStore,
		RuleEvaluator:    s.ruleEvaluator,
		DataSourceStore:  s.dsStore,
		SettingsStore:    s.settingsStore,
		Executor:         s.executor,
		Config:           s.cfg,
		MCPActivityStore: s.mcpActivityStore,
	}

	// Create MCP server with all tools (admin level).
	// Auth is at the connection level — if you have a valid MCP token, you
	// get full access. This matches the stdio behavior for admin users.
	mcpSrv := mcpserver.NewConfiguredServer(deps, true)

	sseServer := server.NewSSEServer(mcpSrv,
		server.WithStaticBasePath("/mcp"),
		server.WithUseFullURLForMessageEndpoint(false),
		server.WithKeepAliveInterval(30*time.Second),
		server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			// Propagate the authenticated user from the auth middleware.
			if user := r.Context().Value(ctxKeyUser); user != nil {
				return context.WithValue(ctx, ctxKeyUser, user)
			}
			return ctx
		}),
	)

	return sseServer
}

// mcpUserFromContext returns the user set by MCPTokenAuth, for role checks.
func mcpUserFromContext(ctx context.Context) *store.User {
	if u, ok := ctx.Value(ctxKeyUser).(*store.User); ok {
		return u
	}
	return nil
}
