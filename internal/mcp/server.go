package mcp

import (
	"context"
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

- overview: status, triage, diagnose, timeline, investigate, changes, settings, notes, delete_note, session_summary
- logs: search, context, attributes, stats, summary, performance, trace, compare
- errors: list, detail, investigate, impact, user_errors, ranking, resolve, ignore
- database: queries, explain, tables, activity, locks, indexes, schema, runbook
- watches: status, create, delete, alerts, dismiss, acknowledge, investigate
- analytics: traffic, endpoints, heatmap, trends, movers
- code: risk, fragile, context, test_gaps, test_priority, annotate_file, annotate_function, hotspots, gen_context, gen_suggest, gen_coverage, deps_service, deps_blast, deps_risk
- deploys: history, impact, record
- healthchecks: list, uptime, create, delete
- connectors: list, get, create, test, update, delete
- servers: list, query, health
- setup: status, detect, guide, verify
- admin: update_retention, users, audit (admin only)

## Follow suggested_tools

Most tool responses include a "suggested_tools" array with pre-filled arguments for the next step. Always prefer following these suggestions over manually constructing the next call — the args are already filled in from the response data.

## Agent memory

Use overview(action: "notes") to save and recall persistent context about services, queries, endpoints, errors, and health checks across sessions. Call overview(action: "notes") at the start of a session to recall previous context. Use overview(action: "session_summary") to save investigation findings for future reference.
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

	// WatchMetrics is not a store — it's a runtime metrics collector.
	WatchMetrics *watcher.WatchMetrics

	// Runtime services — created during Serve()/NewConfiguredServer(), not by callers.
	// Exported so tests can inject them directly.
	ActivityLogger     *ActivityLogger
	SessionTracker     *SessionTracker
	RecurrenceDetector *RecurrenceDetector
	RankingService     *RankingService
	ContextInjector    *ContextInjector
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
func Serve(deps Deps) error {
	// Determine access level.
	isAdmin := true // default: full access (backward compat)
	hasAccess := true
	var authUser *store.User

	if deps.UserStore != nil && deps.MCPToken != "" {
		parentCtx := deps.Ctx
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		user, err := deps.UserStore.GetByMCPToken(ctx, deps.MCPToken)
		cancel()
		if err != nil || user == nil {
			// Invalid token — serve with zero tools.
			hasAccess = false
		} else {
			isAdmin = user.Role == store.RoleAdmin
			authUser = user
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

	// Set up investigation session tracking.
	serverOpts := &mcp.ServerOptions{}
	appCtx := deps.Ctx
	if appCtx == nil {
		appCtx = context.Background()
	}

	if deps.InvestigationSessionStore != nil {
		deps.SessionTracker = NewSessionTracker(appCtx, deps.InvestigationSessionStore, authUser, "stdio")
		deps.RecurrenceDetector = NewRecurrenceDetector(deps.InvestigationSessionStore)

		// Wire session tracking via InitializedHandler.
		st := deps.SessionTracker // capture for closure
		serverOpts.InitializedHandler = func(_ context.Context, req *mcp.InitializedRequest) {
			st.OnInitialize(req)
		}

		// Wire transition and activity stores into session tracker
		if deps.ToolTransitionStore != nil {
			deps.SessionTracker.SetTransitionStore(deps.ToolTransitionStore)
		}
		if deps.MCPActivityStore != nil {
			deps.SessionTracker.SetActivityStore(deps.MCPActivityStore)
		}

		// Wire additional stores into session tracker
		if deps.AgentNoteStore != nil {
			deps.SessionTracker.SetNoteStore(deps.AgentNoteStore)
		}
		if deps.RunbookEffectivenessStore != nil {
			deps.SessionTracker.SetRunbookStore(deps.RunbookEffectivenessStore)
		}
		if deps.QueryMemoryStore != nil {
			deps.SessionTracker.SetQueryMemoryStore(deps.QueryMemoryStore)
		}
		if deps.TrendStore != nil {
			deps.SessionTracker.SetTrendStore(deps.TrendStore)
		}
		if deps.AuditStore != nil {
			deps.SessionTracker.SetAuditStore(deps.AuditStore)
		}

		// Wire code entity + deploy stores into session tracker
		if deps.CodeEntityStore != nil {
			deps.SessionTracker.SetCodeEntityStore(deps.CodeEntityStore)
		}
		if deps.ErrorGroupStore != nil {
			deps.SessionTracker.SetErrorGroupStore(deps.ErrorGroupStore)
		}
		if deps.DeployStore != nil {
			deps.SessionTracker.SetDeployStore(deps.DeployStore)
		}

		// Wire event + test correlation stores into session tracker
		if deps.EventStore != nil {
			deps.SessionTracker.SetEventStore(deps.EventStore)
		}
		if deps.TestCorrelationStore != nil {
			deps.SessionTracker.SetTestCorrelationStore(deps.TestCorrelationStore)
		}
	}

	// Initialize ranking service and context injector
	if deps.ToolTransitionStore != nil {
		deps.RankingService = NewRankingService(deps.ToolTransitionStore, deps.WorkflowTemplateStore)
	}
	if deps.InvestigationSessionStore != nil && deps.ToolTransitionStore != nil {
		deps.ContextInjector = NewContextInjector(deps.InvestigationSessionStore, deps.ToolTransitionStore, ContextInjectorDeps{
			NoteStore:        deps.AgentNoteStore,
			RunbookStore:     deps.RunbookEffectivenessStore,
			QueryMemoryStore: deps.QueryMemoryStore,
			AnalyticsStore:   deps.AnalyticsStore,
			AuditStore:       deps.AuditStore,
			TrendStore:       deps.TrendStore,
			CodeEntityStore:  deps.CodeEntityStore,
			DeployStore:      deps.DeployStore,
		})
	}

	// Seed workflow templates for cold start
	if deps.WorkflowTemplateStore != nil {
		SeedDefaultTemplates(appCtx, deps.WorkflowTemplateStore)
	}

	s := NewConfiguredServer(deps, isAdmin, serverOpts)

	err := s.Run(context.Background(), &mcp.StdioTransport{})

	// Clean up on exit.
	if deps.SessionTracker != nil {
		deps.SessionTracker.CloseSession()
	}
	if deps.ActivityLogger != nil {
		deps.ActivityLogger.Close()
	}

	return err
}

