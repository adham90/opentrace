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
// It tells the agent which tools to call first and how to follow suggestion chains.
const mcpInstructions = `OpenTrace is a self-hosted application monitoring server. You have tools for logs, errors, database stats, health checks, watches (alerts), and agent memory.

## Where to start

- "What's wrong?" / investigating issues → call diagnose (returns errors, logs, performance, watches, healthchecks in one call)
- "System health" / status check → call system_overview
- "What needs attention?" → call triage_alerts (prioritized inbox)
- "Why are queries slow?" → call db_query_stats then follow suggested_tools to explain_query
- Run a full investigation playbook → call runbook with a playbook name (slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike)

## Follow suggested_tools

Most tool responses include a "suggested_tools" array with pre-filled arguments for the next step. Always prefer following these suggestions over manually constructing the next call — the args are already filled in from the response data.

Example chains:
- diagnose → error_detail(fingerprint: "abc") → log_search(exception_class: "NoMethodError") → log_context(log_id: 42)
- log_search(level: "error") → investigate_error(log_id: 42) — one-call deep dive with exception, backtrace, params, SQL, context

## Agent memory

Use add_note / get_notes to save and recall persistent context about services, queries, endpoints, errors, and health checks across sessions. Call get_notes at the start of a session to recall previous context.

## Key tool categories

- Overview: diagnose, system_overview, triage_alerts
- Logs: log_search, log_context, log_stats, log_summary, list_log_attributes
- Errors: error_groups, error_detail, investigate_error, resolve_error, ignore_error
- Database: db_query_stats, explain_query, db_table_stats, db_activity, db_locks, db_index_analysis, schema_overview
- Performance: request_performance, compare_periods, trace_lookup
- Uptime: uptime_status, list_healthchecks, create_healthcheck
- Watches: watch_status, watch, investigate, dismiss_watch
- Runbooks: runbook (composite playbooks that run multiple diagnostics at once)
- Code Intelligence: code_context, whats_fragile, code_risk (source code risk tracking)
- Deploys: deploy_history, deploy_impact, record_deploy (deploy lifecycle + impact measurement)
- Agent Assistant: context (task-specific production context), check_alerts (unified alert view), test_gaps (uncovered error paths), test_priority (error detail for writing tests)
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
	addReadOnlyTools(s, deps, b)

	if isAdmin {
		addWriteTools(s, deps, b)
	}

	// Register MCP resources
	addResources(s, deps)

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
