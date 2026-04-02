// routes.go lists all domain modules and documents the full HTTP route tree.
//
// The router is built in internal/api/server.go (NewServerWithDeps). This file
// registers domain modules that mount additional routes at startup.
//
// ┌─────────────────────────────────────────────────────────────────────┐
// │ HTTP Route Tree                                                    │
// │                                                                    │
// │ Global middleware (applied to every request):                      │
// │   chi/middleware.Logger      – structured request logging          │
// │   chi/middleware.Recoverer   – panic recovery → 500               │
// │   chi/middleware.RequestID   – X-Request-ID header injection       │
// │   PrometheusMiddleware       – request duration + count metrics    │
// │   Compress(5)                – gzip (skipped for /mcp/ SSE)       │
// │   MaxBodySize(10 MB)         – global body limit                  │
// │   ProxyAuth                  – trusted proxy header extraction     │
// │                                                                    │
// │ Root routes (no auth):                                             │
// │   GET  /healthz              – liveness probe                     │
// │   GET  /readyz               – readiness probe (DB ping)          │
// │   GET  /connect              – CLI connect script [auth module]   │
// │                                                                    │
// │ /mcp/* (Streamable HTTP transport, MCP spec 2025-06-18+):         │
// │   middleware: apiLimiter, MCPTokenAuth                             │
// │   POST|GET|DELETE /mcp/*     – MCP Streamable HTTP handler        │
// │                                                                    │
// │ /mcp-sse/* (legacy SSE transport, deprecated):                    │
// │   middleware: apiLimiter, MCPTokenAuth                             │
// │   *  /mcp-sse/*              – MCP SSE handler                    │
// │                                                                    │
// │ /debug/* (admin only):                                             │
// │   middleware: RequireAdmin                                         │
// │   GET /debug/pprof/          – pprof index + handlers             │
// │   GET /debug/metrics         – Prometheus metrics                 │
// │                                                                    │
// │ /api/* (ingestion + domain webhooks):                              │
// │   middleware: DecompressRequest(10 MB), DynamicCORSMiddleware      │
// │                                                                    │
// │   POST /api/logs             – log ingestion                      │
// │     middleware: apiLimiter, DynamicAPIKeyAuth                      │
// │                                                                    │
// │   Domain module routes (all use apiLimiter + DynamicAPIKeyAuth):   │
// │     POST /api/auth/connect     – CLI connect handshake [auth]     │
// │     GET  /api/auth/connect     – connect status check   [auth]    │
// │     POST /api/servers/register – server registration    [servers] │
// │     POST /api/servers/{id}/metrics – metric push        [servers] │
// │                                                                    │
// └─────────────────────────────────────────────────────────────────────┘
//
// Adding a new domain module:
//   1. Create internal/routes/<name>/module.go exporting a server.Module
//   2. Import the package below and append its Module to the slice
//   3. The module's Mount func receives the /api chi.Router and *server.Deps
//      (use deps.APIRouter, deps.RootRouter as needed for route placement)
package main

import (
	authmod "github.com/adham90/opentrace/internal/routes/auth"
	"github.com/adham90/opentrace/internal/routes/servers"
	"github.com/adham90/opentrace/pkg/server"
)

var modules = []server.Module{
	authmod.Module,
	servers.Module,
}
