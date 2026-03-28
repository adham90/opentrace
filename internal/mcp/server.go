package mcp

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/server"

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
}

// NewConfiguredServer creates an MCPServer and registers tools based on the
// access level. When isAdmin is true, both read-only and write tools are
// registered; otherwise only read-only tools are registered.
// This is used by both the stdio transport (Serve) and the SSE transport
// (web server).
func NewConfiguredServer(deps Deps, isAdmin bool, hooks *server.Hooks) *server.MCPServer {
	name := deps.ServerName
	if name == "" {
		name = "opentrace"
	}

	// Set the package-level activity store for tool logging.
	activityStoreForLogging = deps.MCPActivityStore
	if deps.MCPActivityStore != nil {
		alCtx := deps.Ctx
		if alCtx == nil {
			alCtx = context.Background()
		}
		activityLogger = NewActivityLogger(alCtx, deps.MCPActivityStore, 256, 2)
	}

	opts := []server.ServerOption{
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, true),
		server.WithInstructions(mcpInstructions),
	}
	if hooks != nil {
		opts = append(opts, server.WithHooks(hooks))
	}

	s := server.NewMCPServer(
		name,
		"0.1.0",
		opts...,
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

	// Clear the package-level store after registration.
	activityStoreForLogging = nil

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
		s := server.NewMCPServer(name, "0.1.0", server.WithToolCapabilities(false))
		return server.ServeStdio(s)
	}

	// Set up investigation session tracking hooks.
	hooks := &server.Hooks{}
	appCtx := deps.Ctx
	if appCtx == nil {
		appCtx = context.Background()
	}

	if deps.InvestigationSessionStore != nil {
		sessionTracker = NewSessionTracker(appCtx, deps.InvestigationSessionStore, authUser, "stdio")
		sessionTracker.RegisterHooks(hooks)
		recurrenceDetector = NewRecurrenceDetector(deps.InvestigationSessionStore)

		// Stage 3: Wire transition and activity stores into session tracker
		if deps.ToolTransitionStore != nil {
			sessionTracker.SetTransitionStore(deps.ToolTransitionStore)
		}
		if deps.MCPActivityStore != nil {
			sessionTracker.SetActivityStore(deps.MCPActivityStore)
		}

		// Stage 4: Wire additional stores into session tracker
		if deps.AgentNoteStore != nil {
			sessionTracker.SetNoteStore(deps.AgentNoteStore)
		}
		if deps.RunbookEffectivenessStore != nil {
			sessionTracker.SetRunbookStore(deps.RunbookEffectivenessStore)
		}
		if deps.QueryMemoryStore != nil {
			sessionTracker.SetQueryMemoryStore(deps.QueryMemoryStore)
		}
		if deps.TrendStore != nil {
			sessionTracker.SetTrendStore(deps.TrendStore)
		}
		if deps.AuditStore != nil {
			sessionTracker.SetAuditStore(deps.AuditStore)
		}

		// Stage 5: Wire code entity + deploy stores into session tracker
		if deps.CodeEntityStore != nil {
			sessionTracker.SetCodeEntityStore(deps.CodeEntityStore)
		}
		if deps.ErrorGroupStore != nil {
			sessionTracker.SetErrorGroupStore(deps.ErrorGroupStore)
		}
		if deps.DeployStore != nil {
			sessionTracker.SetDeployStore(deps.DeployStore)
		}

		// Stage 6: Wire event + test correlation stores into session tracker
		if deps.EventStore != nil {
			sessionTracker.SetEventStore(deps.EventStore)
		}
		if deps.TestCorrelationStore != nil {
			sessionTracker.SetTestCorrelationStore(deps.TestCorrelationStore)
		}
	}

	// Stage 3: Initialize ranking service and context injector
	if deps.ToolTransitionStore != nil {
		rankingService = NewRankingService(deps.ToolTransitionStore, deps.WorkflowTemplateStore)
	}
	if deps.InvestigationSessionStore != nil && deps.ToolTransitionStore != nil {
		contextInjector = NewContextInjector(deps.InvestigationSessionStore, deps.ToolTransitionStore, ContextInjectorDeps{
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

	s := NewConfiguredServer(deps, isAdmin, hooks)

	err := server.ServeStdio(s)

	// Clean up on exit.
	if sessionTracker != nil {
		sessionTracker.CloseSession()
	}
	if activityLogger != nil {
		activityLogger.Close()
	}

	return err
}

// activityStore is set by NewConfiguredServer when an MCPActivityStore is
// available. It is used by maybeAddTool to wrap handlers with activity logging.
// This is package-level to avoid threading it through every addXxxTools call.
var activityStoreForLogging store.MCPActivityStore
var activityLogger *ActivityLogger

// sessionTracker is set by Serve/NewConfiguredServer when an
// InvestigationSessionStore is available. Used by wrapWithActivityLog to
// tag activity with real session/user identity.
var sessionTracker *SessionTracker

// recurrenceDetector is set alongside sessionTracker when an
// InvestigationSessionStore is available. Used by tool handlers
// to link subsystem entities and detect recurring investigations.
var recurrenceDetector *RecurrenceDetector

// rankingService replaces static tool suggestions with data-driven rankings.
var rankingService *RankingService

// contextInjector enriches tool responses with investigation memory.
var contextInjector *ContextInjector
