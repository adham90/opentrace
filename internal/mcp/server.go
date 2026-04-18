package mcp

import (
	"context"
	"database/sql"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// mcpInstructions is sent to the client during the MCP initialize handshake.
// It tells the agent how to use OpenTrace tools effectively.
const mcpInstructions = `OpenTrace is a self-hosted observability engine. You have tools for logs, errors, database introspection, alerts, code intelligence, deploys, analytics, and more.

## Where to start

- Investigating issues → overview(action: "diagnose")
- System health → overview(action: "status")
- What needs attention → overview(action: "triage")
- Search logs → logs(action: "search", query: "...")
- Check errors → errors(action: "list")
- Before modifying a file → code(action: "annotate_file", path: "the/file.rb")
- Set up SDK for a project → setup(action: "detect") then setup(action: "guide")

## Tools

- overview: status, triage, diagnose, timeline, investigate, changes, settings, notes, delete_note
- logs: search, context, attributes, stats, summary, performance, trace, compare
- errors: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, reopen, new
- database: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions
- watches: status, create, delete, alerts, dismiss, acknowledge, investigate
- analytics: traffic, endpoints, heatmap, trends, movers
- code: risk, fragile, annotate_file, annotate_function, hotspots, gen_context, gen_suggest, deps_service, deps_blast, deps_risk
- healthchecks: list, uptime, create, delete
- connectors: list, get, create, test, update, delete
- servers: list, query, health
- setup: status, detect, guide, verify
- admin: update_retention, users, audit (admin only)

## Follow suggested_tools

Most tool responses include a "suggested_tools" array with pre-filled arguments for the next step. Always prefer following these suggestions over manually constructing the next call — the args are already filled in from the response data.

## Watch conditions

Watches support complex conditions via the "conditions" parameter (JSON object). Condition types:

- threshold: {"type":"threshold","metric":"error_rate","op":"gt","value":0.05,"service":"api"}
- relative: {"type":"relative","metric":"error_rate","op":"gt","baseline_multiple":2.0} (vs baseline)
- delta: {"type":"delta","metric":"error_rate","op":"gt","change_pct":50,"compare_window":"1h"} (rate of change)
- count: {"type":"count","field":"error_fingerprint","distinct":true,"op":"gt","value":10,"window":"1h"} (count distinct values)

Combine with all (AND), any (OR), not:
- {"all":[condition1, condition2]} — all must be true
- {"any":[condition1, condition2]} — at least one must be true
- {"not":condition} — inverts the result

Metrics: error_rate, response_time, p95_response, log_count, error_count, heartbeat, sql_count, cache_hit_rate
Operators: gt, gte, lt, lte, eq, neq

Simple watches still work: watches(action:"create", metric:"error_rate", operator:"gt", threshold:0.05, service:"api")

## Agent memory

Use overview(action: "notes") to save and recall persistent context about services, queries, endpoints, errors, and health checks across sessions. Call overview(action: "notes") at the start of a session to recall previous context.
`

// Deps holds the dependencies for the MCP server.
type Deps struct {
	Ctx        context.Context // app lifecycle context for background workers
	Registry   *connector.Registry
	MCPToken   string // OPENTRACE_MCP_TOKEN from environment
	ServerName string // OPENTRACE_MCP_NAME — custom server name (default: "opentrace")
	Config     *config.Config

	// Stores — embedded so callers can access deps.LogStore directly.
	store.Stores

	// DB is the raw *sql.DB connection for deep capture queries
	// (tables without a formal store interface).
	DB *sql.DB

	// WatchMetrics is not a store — it's a runtime metrics collector.
	WatchMetrics *watcher.WatchMetrics

	// Runtime services — created during Serve()/NewConfiguredServer(), not by callers.
	// Exported so tests can inject them directly.
	ActivityLogger *ActivityLogger
}

// NewConfiguredServer creates an MCPServer and registers tools based on the
// access level. When isAdmin is true, both read-only and write tools are
// registered; otherwise only read-only tools are registered.
// This is used by both the stdio transport (Serve) and the SSE transport
// (web server).
func NewConfiguredServer(deps Deps, isAdmin bool, serverOpts *mcp.ServerOptions) *mcp.Server {
	name := deps.ServerName
	if name == "" {
		name = "opentrace"
	}

	// Initialize activity logger on deps if not already set.
	if deps.ActivityLogger == nil && deps.MCPActivityStore != nil {
		alCtx := deps.Ctx
		if alCtx == nil {
			alCtx = context.Background()
		}
		deps.ActivityLogger = NewActivityLogger(alCtx, deps.MCPActivityStore, 256, 2)
	}

	if serverOpts == nil {
		serverOpts = &mcp.ServerOptions{}
	}
	serverOpts.Instructions = mcpInstructions
	serverOpts.Capabilities = &mcp.ServerCapabilities{
		Tools:     &mcp.ToolCapabilities{ListChanged: false},
		Resources: &mcp.ResourceCapabilities{ListChanged: false, Subscribe: true},
	}

	s := mcp.NewServer(
		&mcp.Implementation{Name: name, Version: "0.1.0"},
		serverOpts,
	)

	b := &CatalogBuilder{}

	// Build the gateway and register the single "opentrace" tool.
	gw := buildGateway(deps, isAdmin, b)
	gatewayTool := gw.Tool()
	gatewayHandler := gw.Handler()

	// Wrap the gateway handler with metrics (tool name = "opentrace").
	gatewayHandler = wrapWithMetrics("opentrace", gatewayHandler)
	s.AddTool(gatewayTool, gatewayHandler)

	// Wire elicitation support (Item 14).
	gw.SetServer(s)

	// Register MCP resources
	addResources(s, deps)

	// Register MCP prompts
	addPrompts(s)

	return s
}

// Serve starts a stdio-based MCP server that exposes all tools from the
// given connector registry plus watcher/alert management tools.
// It blocks until the connection is closed.
//
// When UserStore and MCPToken are provided, the token is validated against the
// user database. Invalid/disabled tokens result in zero tools being registered
// (the server stays alive but is useless). Members get read-only tools; admins
// get all tools. When no UserStore is provided (backward compat), all tools
// are registered.
//
// The authenticated user's EnvScope is attached to the run context so tool
// handlers can honour multi-env authorization via ScopeFromContext. Stdio
// serves a single user for the life of the process, so the scope is fixed
// at startup — unlike the SSE path, there is no per-request middleware.
func Serve(deps Deps) error {
	// Determine access level.
	isAdmin := true // default: full access (backward compat)
	hasAccess := true
	var user *store.User

	if deps.UserStore != nil && deps.MCPToken != "" {
		parentCtx := deps.Ctx
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		u, err := deps.UserStore.GetByMCPToken(ctx, deps.MCPToken)
		cancel()
		if err != nil || u == nil {
			// Invalid token — serve with zero tools.
			hasAccess = false
		} else {
			user = u
			isAdmin = u.Role == store.RoleAdmin
		}
	}

	if !hasAccess {
		// Start server with zero tools — stays alive but useless.
		name := deps.ServerName
		if name == "" {
			name = "opentrace"
		}
		s := mcp.NewServer(
			&mcp.Implementation{Name: name, Version: "0.1.0"},
			&mcp.ServerOptions{
				Capabilities: &mcp.ServerCapabilities{
					Tools: &mcp.ToolCapabilities{ListChanged: false},
				},
			},
		)
		return s.Run(context.Background(), &mcp.StdioTransport{})
	}

	s := NewConfiguredServer(deps, isAdmin, nil)

	// Build the run context with the user's env scope attached so tool
	// handlers can resolve authorization via ScopeFromContext. When no
	// user was loaded (e.g. missing UserStore — backward compat path),
	// the scope is empty and handlers fall back to their own defaults.
	runCtx := context.Background()
	if user != nil {
		runCtx = WithScope(runCtx, ScopeFromUser(user))
	}

	err := s.Run(runCtx, &mcp.StdioTransport{})

	// Clean up on exit.
	if deps.ActivityLogger != nil {
		deps.ActivityLogger.Close()
	}

	return err
}

