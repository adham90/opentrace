package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/tools"
	"github.com/adham90/opentrace/internal/metrics"
	"github.com/adham90/opentrace/internal/store"
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
	Ctx             context.Context // app lifecycle context for background workers
	Registry        *connector.Registry
	ServerStore     store.ServerStore
	MetricStore     store.MetricStore
	UserStore       store.UserStore
	LogStore        store.LogStore
	MCPToken        string // OPENTRACE_MCP_TOKEN from environment
	ServerName      string // OPENTRACE_MCP_NAME — custom server name (default: "opentrace")
	DataSourceStore  store.DataSourceStore
	SettingsStore    store.SettingsStore
	Config           *config.Config
	MCPActivityStore store.MCPActivityStore
	AuditStore       store.AuditStore

	// Error tracking (Sentry-lite)
	ErrorGroupStore store.ErrorGroupStore

	// Uptime / Health Check monitoring
	HealthCheckStore store.HealthCheckStore

	// Agent notes — persistent memory
	AgentNoteStore store.AgentNoteStore

	// Agent-first watches (Phase 1)
	WatchStore    store.WatchStore
	WatchMetrics  *watcher.WatchMetrics

	// Trends + Analytics (Phase 1 features)
	TrendStore     store.TrendStore
	AnalyticsStore store.AnalyticsStore

	// User Journey + Session Timeline (Phase 2 features)
	JourneyStore store.JourneyStore

	// Error Impact (Phase 3 features)
	ErrorImpactStore store.ErrorImpactStore

	// Investigation Memory (Plan 012)
	InvestigationSessionStore store.InvestigationSessionStore

	// Investigation Memory Stage 3 — Ranking + Context
	ToolTransitionStore   store.ToolTransitionStore
	WorkflowTemplateStore store.WorkflowTemplateStore

	// Investigation Memory Stage 4 — All Integrations
	QueryMemoryStore          store.QueryMemoryStore
	RunbookEffectivenessStore store.RunbookEffectivenessStore

	// Investigation Memory Stage 5 — Code Intelligence + Deploys
	CodeEntityStore store.CodeEntityStore
	DeployStore     store.DeployStore

	// Investigation Memory Stage 6 — Agent Assistant
	EventStore           store.EventStore
	TestCorrelationStore store.TestCorrelationStore
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

// rankingServiceAdapter returns a tools.SuggestionRanker that bridges
// the consolidated tools package's SuggestionRanker interface with the
// parent mcp package's RankingService and SessionTracker.
func rankingServiceAdapter() tools.SuggestionRanker {
	if rankingService == nil || sessionTracker == nil {
		return nil
	}
	return &rankerAdapter{}
}

// rankerAdapter implements tools.SuggestionRanker using the package-level
// rankingService and sessionTracker.
type rankerAdapter struct{}

func (r *rankerAdapter) RankAndTrack(suggestions []tools.ToolSuggestion) []tools.ToolSuggestion {
	if rankingService == nil || sessionTracker == nil {
		return suggestions
	}
	sess := sessionTracker.CurrentSession()
	if sess == nil {
		return suggestions
	}

	// Convert tools.ToolSuggestion → mcp.ToolSuggestion for ranking.
	mcpSuggestions := make([]ToolSuggestion, len(suggestions))
	for i, s := range suggestions {
		mcpSuggestions[i] = ToolSuggestion{
			Tool:       s.Tool,
			Why:        s.Why,
			Args:       s.Args,
			Confidence: s.Confidence,
			Source:     s.Source,
			Evidence:   s.Evidence,
		}
	}

	currentTool := ""
	if len(sess.ToolSequence) > 0 {
		currentTool = sess.ToolSequence[len(sess.ToolSequence)-1]
	}
	ranked := rankingService.RankSuggestions(context.Background(), RankingRequest{
		CurrentTool:         currentTool,
		Intent:              sess.Intent,
		StepIndex:           sess.TotalSteps,
		SessionTools:        sess.ToolSequence,
		FallbackSuggestions: mcpSuggestions,
	})

	if len(ranked) == 0 {
		return suggestions
	}

	// Convert back to tools.ToolSuggestion.
	result := make([]tools.ToolSuggestion, len(ranked))
	for i, s := range ranked {
		result[i] = tools.ToolSuggestion{
			Tool:       s.Tool,
			Why:        s.Why,
			Args:       s.Args,
			Confidence: s.Confidence,
			Source:     s.Source,
			Evidence:   s.Evidence,
		}
	}

	// Track for acceptance detection.
	sessionTracker.SetLastSuggestions(mcpSuggestions)

	return result
}

// maybeAddTool registers a tool on the MCP server if s is non-nil.
// When s is nil (catalog-only mode), this is a no-op.
// If activity logging is enabled, wraps the handler to record tool calls.
func maybeAddTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	if s != nil {
		// Wrap with Prometheus metrics recording (always active).
		handler = wrapWithMetrics(tool.Name, handler)
		if activityStoreForLogging != nil {
			handler = wrapWithActivityLog(activityStoreForLogging, tool.Name, handler)
		}
		s.AddTool(tool, handler)
	}
}

// wrapWithMetrics wraps a tool handler to record Prometheus metrics for each call.
func wrapWithMetrics(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		metrics.RecordMCPToolCall(toolName)
		return handler(ctx, request)
	}
}

// wrapWithActivityLog wraps a tool handler to log its execution to the activity store.
func wrapWithActivityLog(as store.MCPActivityStore, toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Snapshot the suggestions from the PREVIOUS tool's response before this
		// handler runs and overwrites them with its own suggestions.
		var priorSuggestions []ToolSuggestion
		if sessionTracker != nil {
			priorSuggestions = sessionTracker.SnapshotLastSuggestions()
		}

		start := time.Now()
		result, err := handler(ctx, request)
		elapsed := time.Since(start).Milliseconds()

		// Build a brief preview of args
		argsPreview := ""
		if args := request.GetArguments(); len(args) > 0 {
			data, _ := json.Marshal(args)
			argsPreview = string(data)
			if len(argsPreview) > 500 {
				argsPreview = argsPreview[:500]
			}
		}

		// Build result preview
		isError := err != nil
		resultPreview := ""
		if result != nil && len(result.Content) > 0 {
			if txt, ok := result.Content[0].(mcp.TextContent); ok {
				resultPreview = txt.Text
				if len(resultPreview) > 500 {
					resultPreview = resultPreview[:500]
				}
			}
			isError = isError || result.IsError
		}

		// Get real identity from session tracker.
		sessionID := "mcp"
		userID := ""
		invSessionID := ""
		stepIndex := 0
		if sessionTracker != nil {
			if uid := sessionTracker.UserID(); uid != "" {
				userID = uid
			}
			if sid := sessionTracker.CurrentSessionID(); sid != "" {
				invSessionID = sid
				sessionID = sid
			}
			stepIndex = sessionTracker.RecordStep(toolName, isError, priorSuggestions)
		}

		// Check if this tool was in the prior suggestions (before handler overwrote them).
		wasSuggested := false
		suggestionRank := 0
		for i, s := range priorSuggestions {
			if s.Tool == toolName {
				wasSuggested = true
				suggestionRank = i + 1
				break
			}
		}

		// Log via bounded activity logger to avoid unbounded goroutine growth.
		// WasSuggested/SuggestionRank and PreviousStepIndex are included so the
		// Log method can handle both INSERT and previous-step UPDATE atomically,
		// avoiding races between async INSERT and sync UPDATE.
		if activityLogger != nil {
			prevStep := 0
			if stepIndex > 1 {
				prevStep = stepIndex - 1
			}
			activityLogger.Log(store.LogMCPActivityParams{
				SessionID:              sessionID,
				UserID:                 userID,
				ToolName:               toolName,
				Arguments:              argsPreview,
				ResultPreview:          resultPreview,
				IsError:                isError,
				DurationMs:             &elapsed,
				EventType:              "tool_call",
				InvestigationSessionID: invSessionID,
				StepIndex:              stepIndex,
				WasSuggested:           wasSuggested,
				SuggestionRank:         suggestionRank,
				PreviousStepIndex:      prevStep,
			})
		}

		// Inject investigation context for investigation-intent sessions.
		if contextInjector != nil && sessionTracker != nil && result != nil && !result.IsError && len(result.Content) > 0 {
			if sess := sessionTracker.CurrentSession(); sess != nil && sess.Intent == IntentInvestigation {
				if txt, ok := result.Content[0].(mcp.TextContent); ok {
					if enriched := InjectContextIntoResult(contextInjector, sess, toolName, txt.Text); enriched != txt.Text {
						result.Content[0] = mcp.NewTextContent(enriched)
					}
				}
			}
		}

		return result, err
	}
}

// addReadOnlyTools registers read-only tools available to all users.
// When s is nil (catalog-only mode), tools are only cataloged via b.
func addReadOnlyTools(s *server.MCPServer, deps Deps, b *CatalogBuilder) {
	// -----------------------------------------------------------------------
	// Consolidated: connectors (read-only actions: list, get)
	// -----------------------------------------------------------------------
	maybeAddTool(s, tools.ConnectorsTool(), tools.ConnectorsHandler(tools.ConnectorsDeps{
		DSStore:       deps.DataSourceStore,
		Registry:      deps.Registry,
		LogStore:      deps.LogStore,
		Config:        deps.Config,
		SettingsStore: deps.SettingsStore,
	}))
	b.Add("connectors", "Connector management: list, get, create, test, update, delete", "Connectors", "read", "")

	// -----------------------------------------------------------------------
	// Consolidated: database (read-only actions: queries, tables, activity, locks, connections, indexes, schema, storage, long_transactions)
	// -----------------------------------------------------------------------
	maybeAddTool(s, tools.DatabaseTool(), tools.DatabaseHandler(tools.DatabaseDeps{
		Registry:         deps.Registry,
		QueryMemoryStore: deps.QueryMemoryStore,
	}))
	b.Add("database", "Database introspection and management: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions", "Database Introspection", "read", "database connector")

	// -----------------------------------------------------------------------
	// Consolidated: runbook
	// -----------------------------------------------------------------------
	if deps.Registry != nil {
		maybeAddTool(s, tools.RunbookTool(), tools.RunbookHandler(tools.RunbookDeps{
			Registry:                  deps.Registry,
			LogStore:                  deps.LogStore,
			RunbookEffectivenessStore: deps.RunbookEffectivenessStore,
		}))
		b.Add("runbook", "Run a composite investigation playbook (slow_database, connection_exhaustion, etc.)", "Database Introspection", "read", "database connector")
	}

	// -----------------------------------------------------------------------
	// Consolidated: logs
	// -----------------------------------------------------------------------
	if deps.LogStore != nil {
		var traceRecorder tools.TraceSessionRecorder
		if sessionTracker != nil {
			traceRecorder = func(traceID string) {
				if sess := sessionTracker.CurrentSession(); sess != nil {
					traceIDs := append([]string{}, sess.TraceIDs...)
					traceIDs = append(traceIDs, traceID)
					sessionTracker.UpdateSession(store.UpdateInvestigationSessionParams{
						TraceIDs: traceIDs,
					})
				}
			}
		}
		maybeAddTool(s, tools.LogsTool(), tools.LogsHandler(tools.LogsDeps{
			LogStore:             deps.LogStore,
			ErrorGroupStore:      deps.ErrorGroupStore,
			TraceSessionRecorder: traceRecorder,
			Ranker:               rankingServiceAdapter(),
		}))
		b.Add("logs", "Unified log intelligence: search, context, attributes, stats, summary, performance, trace, compare", "Log Intelligence", "read", "")
	}

	// -----------------------------------------------------------------------
	// Consolidated: errors (read-only actions: list, detail, investigate, impact, user_errors, ranking)
	// -----------------------------------------------------------------------
	if deps.ErrorGroupStore != nil || deps.LogStore != nil || deps.ErrorImpactStore != nil {
		var sess tools.SessionInfo
		var rec tools.RecurrenceLinker
		if sessionTracker != nil {
			sess = sessionTracker
		}
		if recurrenceDetector != nil {
			rec = recurrenceDetector
		}
		maybeAddTool(s, tools.ErrorsTool(), tools.ErrorsHandler(tools.ErrorsDeps{
			ErrorGroupStore:  deps.ErrorGroupStore,
			LogStore:         deps.LogStore,
			ErrorImpactStore: deps.ErrorImpactStore,
			Session:          sess,
			Recurrence:       rec,
			Ranker:           rankingServiceAdapter(),
		}))
		b.Add("errors", "Manage and investigate errors: list, detail, investigate, impact, user_errors, ranking, resolve, ignore", "Errors", "read", "")
	}

	// -----------------------------------------------------------------------
	// Consolidated: healthchecks (read-only actions: list, uptime)
	// -----------------------------------------------------------------------
	if deps.HealthCheckStore != nil {
		maybeAddTool(s, tools.HealthchecksTool(), tools.HealthchecksHandler(tools.HealthchecksDeps{
			HealthCheckStore: deps.HealthCheckStore,
		}))
		b.Add("healthchecks", "Health check management: list, uptime, create, delete", "Uptime", "read", "")
	}

	// -----------------------------------------------------------------------
	// Consolidated: overview (status, triage, diagnose, timeline)
	// -----------------------------------------------------------------------
	maybeAddTool(s, tools.OverviewTool(), tools.OverviewHandler(tools.OverviewDeps{
		LogStore:         deps.LogStore,
		DSStore:          deps.DataSourceStore,
		ServerStore:      deps.ServerStore,
		ErrorGroupStore:  deps.ErrorGroupStore,
		WatchStore:       deps.WatchStore,
		HealthCheckStore: deps.HealthCheckStore,
	}))
	b.Add("overview", "System overview, triage, diagnosis, and incident timeline", "Overview", "read", "")

	// -----------------------------------------------------------------------
	// Consolidated: watches (read-only actions: status, alerts, investigate)
	// -----------------------------------------------------------------------
	if deps.WatchStore != nil {
		maybeAddTool(s, tools.WatchesTool(), tools.WatchesHandler(tools.WatchesDeps{
			WatchStore:   deps.WatchStore,
			LogStore:     deps.LogStore,
			WatchMetrics: deps.WatchMetrics,
		}))
		b.Add("watches", "Watch management: status, create, delete, alerts, dismiss, acknowledge, investigate", "Watches", "read", "")
	}

	// -----------------------------------------------------------------------
	// Consolidated: analytics (traffic, endpoints, heatmap, trends, movers)
	// -----------------------------------------------------------------------
	if deps.AnalyticsStore != nil || deps.TrendStore != nil {
		maybeAddTool(s, tools.AnalyticsTool(), tools.AnalyticsHandler(tools.AnalyticsDeps{
			AnalyticsStore: deps.AnalyticsStore,
			TrendStore:     deps.TrendStore,
		}))
		b.Add("analytics", "Web analytics and trends: traffic, endpoints, heatmap, trends, movers", "Analytics", "read", "")
	}

	// -----------------------------------------------------------------------
	// Consolidated: journeys (sessions, paths, funnels, timeline, waterfall)
	// -----------------------------------------------------------------------
	if deps.JourneyStore != nil {
		maybeAddTool(s, tools.JourneysTool(), tools.JourneysHandler(tools.JourneysDeps{
			JourneyStore: deps.JourneyStore,
		}))
		b.Add("journeys", "User journey analysis: sessions, paths, funnels, timeline, waterfall", "Journey", "read", "")
	}

	// -----------------------------------------------------------------------
	// Consolidated: code_intel (risk, fragile, context, test_gaps, test_priority)
	// -----------------------------------------------------------------------
	if deps.CodeEntityStore != nil || deps.TestCorrelationStore != nil {
		maybeAddTool(s, tools.CodeIntelTool(), tools.CodeIntelHandler(tools.CodeIntelDeps{
			CodeEntityStore:          deps.CodeEntityStore,
			ErrorGroupStore:          deps.ErrorGroupStore,
			TestCorrelationStore:     deps.TestCorrelationStore,
			DeployStore:              deps.DeployStore,
			AgentNoteStore:           deps.AgentNoteStore,
			InvestigationSessionStore: deps.InvestigationSessionStore,
		}))
		b.Add("code_intel", "Code intelligence: risk scores, fragile code, error context, test gaps", "Code Intelligence", "read", "")
	}

	// -----------------------------------------------------------------------
	// Consolidated: deploys (read-only actions: history, impact)
	// -----------------------------------------------------------------------
	if deps.DeployStore != nil {
		maybeAddTool(s, tools.DeploysTool(), tools.DeploysHandler(tools.DeploysDeps{
			DeployStore: deps.DeployStore,
		}))
		b.Add("deploys", "Deploy management: history, impact, record", "Deploys", "read", "")
	}

	// -----------------------------------------------------------------------
	// Server metrics (not consolidated — kept as individual tools)
	// -----------------------------------------------------------------------
	if deps.ServerStore != nil && deps.MetricStore != nil {
		maybeAddTool(s,
			mcp.NewTool("list_servers",
				mcp.WithDescription("List all monitored servers with their status (online/offline/unknown)"),
			),
			listServersHandler(deps.ServerStore),
		)
		b.Add("list_servers", "List all monitored servers with their status", "Server Metrics", "read", "")

		maybeAddTool(s,
			mcp.NewTool("query_metrics",
				mcp.WithDescription("Query time-series metrics for a server (CPU, memory, disk, network, load)"),
				mcp.WithString("server_id", mcp.Required(), mcp.Description("Server UUID (from list_servers)")),
				mcp.WithString("metric_name", mcp.Description("Metric name filter (e.g. cpu.usage_percent, memory.usage_percent)")),
				mcp.WithString("start", mcp.Description("Start time in ISO 8601 format")),
				mcp.WithString("end", mcp.Description("End time in ISO 8601 format")),
				mcp.WithNumber("limit", mcp.Description("Max results (default: 100)")),
			),
			queryMetricsHandler(deps.ServerStore, deps.MetricStore),
		)
		b.Add("query_metrics", "Query time-series metrics for a server (CPU, memory, disk, network, load)", "Server Metrics", "read", "")

		maybeAddTool(s,
			mcp.NewTool("server_health",
				mcp.WithDescription("Get current health snapshot for a server — latest value for every metric"),
				mcp.WithString("server_id", mcp.Required(), mcp.Description("Server UUID (from list_servers)")),
			),
			serverHealthHandler(deps.ServerStore, deps.MetricStore),
		)
		b.Add("server_health", "Get current health snapshot for a server", "Server Metrics", "read", "")
	}
}

// addWriteTools registers write/admin tools (connector tools, create_watcher, preview_watcher).
// When s is nil (catalog-only mode), tools are only cataloged via b.
func addWriteTools(s *server.MCPServer, deps Deps, b *CatalogBuilder) {
	// Dynamic connector tools (run queries, etc.) — NOT consolidated.
	// Note: dynamic connector tools are not cataloged here — the web handler
	// merges them at request time from s.registry.AllTools().
	for _, t := range deps.Registry.AllTools() {
		maybeAddTool(s, convertTool(t), bridgeHandler(t))
	}

	// -----------------------------------------------------------------------
	// Consolidated: admin (settings, users, audit, notes, retention)
	// -----------------------------------------------------------------------
	maybeAddTool(s, tools.AdminTool(), tools.AdminHandler(tools.AdminDeps{
		SettingsStore:    deps.SettingsStore,
		UserStore:        deps.UserStore,
		AuditStore:       deps.AuditStore,
		AgentNoteStore:   deps.AgentNoteStore,
		MCPActivityStore: deps.MCPActivityStore,
		Registry:         deps.Registry,
	}))
	b.Add("admin", "Admin operations: settings, users, audit, notes, retention, activity", "Admin", "admin", "")

	// -----------------------------------------------------------------------
	// Investigation session summary (not consolidated — kept as individual tool)
	// -----------------------------------------------------------------------
	if deps.InvestigationSessionStore != nil {
		maybeAddTool(s,
			mcp.NewTool("set_session_summary",
				mcp.WithDescription(
					"Provide a summary of the current investigation session. "+
						"Call this when you've completed an investigation or identified a root cause. "+
						"This helps future investigations of similar issues.",
				),
				mcp.WithString("summary", mcp.Required(),
					mcp.Description("One sentence describing what was investigated and found")),
				mcp.WithString("root_cause",
					mcp.Description("The root cause if identified")),
				mcp.WithString("fix_applied",
					mcp.Description("What fix was applied, if any")),
				mcp.WithString("outcome",
					mcp.Description("Session outcome: resolved, unresolved, or partial")),
				mcp.WithString("primary_service",
					mcp.Description("The primary service investigated (e.g., 'payments', 'auth')")),
			),
			setSessionSummaryHandler(),
		)
		b.Add("set_session_summary", "Save a summary of the current investigation for future reference", "Investigation Memory", "admin", "")
	}
}

// convertTool maps an connector.Tool to an mcp.Tool with the appropriate
// JSON Schema properties derived from the tool's parameter definitions.
func convertTool(t connector.Tool) mcp.Tool {
	opts := []mcp.ToolOption{
		mcp.WithDescription(t.Description),
	}

	for _, p := range t.Params {
		var propOpts []mcp.PropertyOption
		if p.Required {
			propOpts = append(propOpts, mcp.Required())
		}

		switch p.Type {
		case "string":
			opts = append(opts, mcp.WithString(p.Name, propOpts...))
		case "int":
			opts = append(opts, mcp.WithNumber(p.Name, propOpts...))
		case "bool":
			opts = append(opts, mcp.WithBoolean(p.Name, propOpts...))
		default:
			opts = append(opts, mcp.WithString(p.Name, propOpts...))
		}
	}

	return mcp.NewTool(t.Name, opts...)
}

// bridgeHandler wraps an connector.Tool handler as an MCP ToolHandlerFunc.
// Tool-level errors are returned as MCP error results (not transport errors).
func bridgeHandler(t connector.Tool) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		if args == nil {
			args = make(map[string]any)
		}

		result, err := t.Handler(ctx, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(result), nil
	}
}

// listConnectorsHandler returns a handler that lists connectors.
// When a DataSourceStore is available, returns full connector details from the
// database with optional type filter. Falls back to listing
// active registry tools when no store is provided.
func listConnectorsHandler(registry *connector.Registry, dsStore store.DataSourceStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		// When we have a store, return rich connector info.
		if dsStore != nil {
			var params store.ListDataSourceParams
			if v, ok := args["type"].(string); ok && v != "" {
				params.Type = store.ConnectorType(v)
			}

			connectors, err := dsStore.List(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to list connectors: %v", err)), nil
			}
			if len(connectors) == 0 {
				return mcp.NewToolResultText("No connectors found."), nil
			}

			// Build response with connector details + active tools.
			type connectorEntry struct {
				ID            string   `json:"id"`
				Name          string   `json:"name"`
				Type          string   `json:"type"`
				Status        string   `json:"status"`
				StatusMessage string   `json:"status_message,omitempty"`
				LastTestedAt  string   `json:"last_tested_at,omitempty"`
				ActiveTools   []string `json:"active_tools,omitempty"`
			}

			// Collect active tool names for reference.
			activeToolNames := make([]string, 0)
			for _, t := range registry.AllTools() {
				activeToolNames = append(activeToolNames, t.Name)
			}

			entries := make([]connectorEntry, 0, len(connectors))
			for _, c := range connectors {
				e := connectorEntry{
					ID:     c.ID.String(),
					Name:   c.Name,
					Type:   string(c.Type),
					Status: string(c.Status),
				}
				if c.StatusMessage != nil {
					e.StatusMessage = *c.StatusMessage
				}
				if c.LastTestedAt != nil {
					e.LastTestedAt = c.LastTestedAt.Format(time.RFC3339)
				}
				// Include active tools if this connector is connected.
				if c.Status == store.StatusConnected {
					e.ActiveTools = activeToolNames
				}
				entries = append(entries, e)
			}

			data, err := json.Marshal(entries)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal connectors: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		}

		// Fallback: no store, just list active registry tools.
		tools := registry.AllTools()
		if len(tools) == 0 {
			return mcp.NewToolResultText("No connectors are currently active."), nil
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("Active tools (%d):\n", len(tools)))
		for _, t := range tools {
			b.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
		}

		return mcp.NewToolResultText(b.String()), nil
	}
}

// listServersHandler returns a handler that lists all monitored servers.
func listServersHandler(ss store.ServerStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		servers, err := ss.List(ctx, store.ListServerParams{})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list servers: %v", err)), nil
		}
		if len(servers) == 0 {
			return mcp.NewToolResultText("No monitored servers."), nil
		}
		data, err := json.Marshal(servers)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal servers: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// queryMetricsHandler returns a handler that queries time-series metrics.
func queryMetricsHandler(ss store.ServerStore, ms store.MetricStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		serverIDStr, _ := args["server_id"].(string)
		if serverIDStr == "" {
			return mcp.NewToolResultError("server_id is required"), nil
		}

		serverID, err := uuid.Parse(serverIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid server_id format"), nil
		}

		q := store.MetricQuery{ServerID: serverID}
		if v, ok := args["metric_name"].(string); ok {
			q.MetricName = v
		}
		if v, ok := args["start"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				q.Start = &t
			}
		}
		if v, ok := args["end"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				q.End = &t
			}
		}
		if v, ok := args["limit"].(float64); ok && v > 0 {
			q.Limit = int(v)
		}

		points, err := ms.Query(ctx, q)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
		}
		if len(points) == 0 {
			return mcp.NewToolResultText("No metrics found matching the given criteria."), nil
		}

		data, err := json.Marshal(points)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal metrics: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// serverHealthHandler returns a handler that shows the latest metrics for a server.
func serverHealthHandler(ss store.ServerStore, ms store.MetricStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		serverIDStr, _ := args["server_id"].(string)
		if serverIDStr == "" {
			return mcp.NewToolResultError("server_id is required"), nil
		}

		serverID, err := uuid.Parse(serverIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid server_id format"), nil
		}

		srv, err := ss.GetByID(ctx, serverID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("server not found: %v", err)), nil
		}

		latest, err := ms.LatestByServer(ctx, serverID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
		}

		result := map[string]any{
			"server":  srv,
			"metrics": latest,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
