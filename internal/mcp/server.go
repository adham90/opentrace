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
	}

	// Stage 3: Initialize ranking service and context injector
	if deps.ToolTransitionStore != nil {
		rankingService = NewRankingService(deps.ToolTransitionStore, deps.WorkflowTemplateStore)
	}
	if deps.InvestigationSessionStore != nil && deps.ToolTransitionStore != nil {
		contextInjector = NewContextInjector(deps.InvestigationSessionStore, deps.ToolTransitionStore)
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
			stepIndex = sessionTracker.RecordStep(toolName, isError)
		}

		// Log via bounded activity logger to avoid unbounded goroutine growth
		if activityLogger != nil {
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
	// Meta-tool for listing connectors.
	maybeAddTool(s,
		mcp.NewTool("list_connectors",
			mcp.WithDescription("List all OpenTrace connectors with their status, config, and available tools. When a DataSourceStore is available, returns full connector details from the database; otherwise lists active registry tools."),
			mcp.WithString("type", mcp.Description("Filter by connector type: database, logs, monitoring, server_metrics")),
		),
		listConnectorsHandler(deps.Registry, deps.DataSourceStore),
	)
	b.Add("list_connectors", "List all OpenTrace connectors with status, config, and tools", "Connectors", "read", "")

	// Get single connector details (read-only).
	if deps.DataSourceStore != nil {
		maybeAddTool(s,
			mcp.NewTool("get_connector",
				mcp.WithDescription("Get full details for a specific connector: type, status, config, and last test time. Use to inspect a connector's configuration or diagnose connection issues."),
				mcp.WithString("connector_id", mcp.Required(), mcp.Description("Connector UUID (from list_connectors)")),
			),
			getConnectorHandler(deps.DataSourceStore),
		)
		b.Add("get_connector", "Get full details for a specific connector", "Connectors", "read", "")
	}

	// Database introspection tools (Postgres runtime stats).
	maybeAddTool(s,
		mcp.NewTool("db_query_stats",
			mcp.WithDescription("Show top SQL queries from pg_stat_statements — useful for identifying slow or frequent queries to monitor"),
			mcp.WithString("order_by", mcp.Description("Sort by: calls, total_exec_time (default), mean_exec_time, rows, shared_blks_hit, shared_blks_read")),
			mcp.WithNumber("limit", mcp.Description("Number of queries to return (default: 20, max: 100)")),
			mcp.WithString("filter", mcp.Description("Filter queries by pattern (case-insensitive substring match on query text)")),
		),
		queryStatsHandler(deps.Registry),
	)
	b.Add("db_query_stats", "Show top SQL queries from pg_stat_statements", "Database Introspection", "read", "database connector")

	maybeAddTool(s,
		mcp.NewTool("db_table_stats",
			mcp.WithDescription("Show table-level statistics: row counts, dead tuples, sequential vs index scans, cache hit ratios, and vacuum status"),
			mcp.WithString("table_name", mcp.Description("Filter to a specific table name")),
		),
		dbTableStatsHandler(deps.Registry),
	)
	b.Add("db_table_stats", "Show table-level statistics: row counts, dead tuples, scans, cache hits", "Database Introspection", "read", "database connector")

	maybeAddTool(s,
		mcp.NewTool("db_activity",
			mcp.WithDescription("Show current database activity: connection summary, long-running queries (>10s), idle-in-transaction sessions (>1min), and connection utilization"),
		),
		dbActivityHandler(deps.Registry),
	)
	b.Add("db_activity", "Show current database activity: connections, long-running queries, idle sessions", "Database Introspection", "read", "database connector")

	// Lock contention (read-only — queries system catalogs).
	maybeAddTool(s,
		mcp.NewTool("db_locks",
			mcp.WithDescription("Show current lock contention: blocking chains, lock types, and waiting queries. Use when db_activity shows long-running or idle-in-transaction sessions, or when users report the database is stuck."),
			mcp.WithBoolean("blocking_only", mcp.Description("Only show lock chains where one query is blocking another (default: true). Set to false to see all held locks.")),
		),
		dbLocksHandler(deps.Registry),
	)
	b.Add("db_locks", "Show current database locks, blocking chains, and deadlock risks", "Database Introspection", "read", "database connector")

	// Log aggregation and pattern detection.
	if deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("log_stats",
				mcp.WithDescription("Aggregate log statistics: volume by level/service, error rate trends, and most common error patterns. Use when investigating 'what's going wrong?', 'are errors increasing?', or 'which service has the most issues?'. Unlike log_search which returns individual entries, this returns aggregated counts and patterns."),
				mcp.WithString("time_range", mcp.Description("Lookback window: '15m', '1h' (default), '6h', '24h', '7d'")),
				mcp.WithString("group_by", mcp.Description("Primary grouping: 'level' (default), 'service', 'pattern' (clusters similar error messages)")),
				mcp.WithString("service", mcp.Description("Filter to a specific service name")),
				mcp.WithString("level", mcp.Description("Filter to a specific log level (debug, info, warn, error, fatal)")),
				mcp.WithString("bucket_interval", mcp.Description("Time bucket size for trend data: '1m', '5m' (default), '15m', '1h'")),
			),
			logStatsHandler(deps.LogStore),
		)
		b.Add("log_stats", "Aggregate log statistics by level, service, or pattern with trend detection", "Log Intelligence", "read", "")
	}

	// Request performance analysis (N+1 detection, slow endpoints).
	if deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("request_performance",
				mcp.WithDescription("Analyze HTTP request performance: find N+1 queries, slow endpoints, SQL-heavy requests. Queries the request_summaries table populated by the Ruby gem's RequestCollector. Use when investigating 'why is this endpoint slow?', 'are there N+1 queries?', or 'which requests make the most SQL calls?'."),
				mcp.WithString("time_range", mcp.Description("Lookback window: '1h', '24h' (default), '7d'")),
				mcp.WithString("controller", mcp.Description("Filter by controller name (partial match)")),
				mcp.WithString("action", mcp.Description("Filter by action name (exact match)")),
				mcp.WithString("path", mcp.Description("Filter by request path (partial match)")),
				mcp.WithBoolean("n_plus_one_only", mcp.Description("Only show requests with N+1 query issues (default: false)")),
				mcp.WithNumber("min_duration_ms", mcp.Description("Minimum request duration in milliseconds")),
				mcp.WithNumber("min_sql_count", mcp.Description("Minimum number of SQL queries in request")),
				mcp.WithString("sort_by", mcp.Description("Sort by: 'duration_ms' (default), 'sql_count', 'db_time_ms', 'duplicate_queries'")),
				mcp.WithNumber("limit", mcp.Description("Maximum results to return (default: 20, max: 100)")),
			),
			requestPerformanceHandler(deps.LogStore),
		)
		b.Add("request_performance", "Analyze request performance: N+1 queries, slow endpoints, SQL-heavy requests", "Log Intelligence", "read", "")
	}

	// Distributed trace lookup.
	if deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("trace_lookup",
				mcp.WithDescription("Follow a distributed trace across services. Given a trace ID, assembles all log entries from that trace ordered by timestamp, showing the request journey through services, timing between hops, and where errors occurred. Use when investigating a specific request failure or latency issue."),
				mcp.WithString("trace_id", mcp.Required(), mcp.Description("The trace/correlation ID to look up (from log entries or error reports)")),
				mcp.WithBoolean("include_context", mcp.Description("Include surrounding log entries (+/- 2 seconds) from each service for additional context (default: false)")),
			),
			traceLookupHandler(deps.LogStore),
		)
		b.Add("trace_lookup", "Assemble a distributed trace timeline from log entries by trace ID", "Log Intelligence", "read", "")
	}

	// Index health analysis (read-only — queries system catalogs).
	maybeAddTool(s,
		mcp.NewTool("db_index_analysis",
			mcp.WithDescription("Analyze database index health: find unused indexes (wasting disk/write overhead), missing indexes (tables with high sequential scan ratios), duplicate indexes, and bloated indexes. Use after db_table_stats shows sequential scans or db_query_stats shows slow queries."),
			mcp.WithString("table_name", mcp.Description("Analyze indexes for a specific table (omit for all tables)")),
			mcp.WithBoolean("include_suggestions", mcp.Description("Include CREATE/DROP INDEX suggestions (default: true)")),
		),
		dbIndexAnalysisHandler(deps.Registry),
	)
	b.Add("db_index_analysis", "Analyze indexes: find unused, missing, duplicate, and bloated indexes with fix suggestions", "Database Introspection", "read", "database connector")

	// Period comparison (read-only — uses log/alert stores).
	if deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("compare_periods",
				mcp.WithDescription("Compare metrics between two time periods to identify what changed. Compares error rates, log volumes, or alert counts between a current period and a baseline. Use when the user asks 'what changed?', 'why is it slow now?', or 'is this worse than yesterday?'."),
				mcp.WithString("metric", mcp.Required(), mcp.Description("What to compare: 'errors' (log error rates), 'log_volume' (total log counts by level), 'alerts' (alert counts by severity)")),
				mcp.WithString("current_period", mcp.Description("Current period: 'last_1h' (default), 'last_6h', 'last_24h', 'today'")),
				mcp.WithString("baseline_period", mcp.Description("Baseline to compare against: 'previous' (default), 'yesterday_same_time', 'last_week_same_time'")),
				mcp.WithString("service", mcp.Description("Filter to a specific service (for error/log_volume metrics)")),
			),
			comparePeriodsHandler(deps.LogStore),
		)
		b.Add("compare_periods", "Compare error rates, log volume, or alert counts between two time periods", "Log Intelligence", "read", "")
	}

	// Server metrics read tools.
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

	// Connection pool stats (read-only — queries pg_stat_activity).
	maybeAddTool(s,
		mcp.NewTool("connection_pool_stats",
			mcp.WithDescription("Show connection pool health: current utilization, idle/active connections, wait queue depth, and per-application breakdown. Use when diagnosing 'database is slow' or 'connection timeout' issues."),
		),
		connectionPoolStatsHandler(deps.Registry),
	)
	b.Add("connection_pool_stats", "Show connection pool health: utilization, per-application breakdown, and warnings", "Database Introspection", "read", "database connector")

	// Replication status (read-only — queries pg_stat_replication and related views).
	maybeAddTool(s,
		mcp.NewTool("replication_status",
			mcp.WithDescription("Check PostgreSQL replication health: server role (primary/replica), connected replicas, replication lag, slot status, and WAL archival. Use when investigating 'replica is behind', 'replication slot bloat', or 'WAL archive failures'."),
		),
		replicationStatusHandler(deps.Registry),
	)
	b.Add("replication_status", "Check replication lag, slot status, connected replicas, and WAL archival health", "Database Introspection", "read", "database connector")

	// Log search (read-only — searches log entries).
	if deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("log_search",
				mcp.WithDescription("Search log entries with full-text search and filters. Returns individual log entries (unlike log_stats which returns aggregated counts). Use when you need to find specific log messages, investigate errors, or look up events by trace ID. If FTS query returns nothing, automatically falls back to matching against service names."),
				mcp.WithString("query", mcp.Description("Full-text search query (searches message content). Also tries matching service names if no FTS results found.")),
				mcp.WithString("service", mcp.Description("Filter by service name")),
				mcp.WithString("level", mcp.Description("Filter by log level: debug, info, warn, error, fatal (comma-separated for multiple)")),
				mcp.WithString("environment", mcp.Description("Filter by deployment environment (e.g. production, staging, development)")),
				mcp.WithString("commit_hash", mcp.Description("Filter by git commit hash. Supports short hashes (prefix match) and full 40-char SHA. Use to find all logs from a specific deploy.")),
				mcp.WithString("trace_id", mcp.Description("Filter by trace/correlation ID")),
				mcp.WithString("request_id", mcp.Description("Filter by request ID to see all logs from a single HTTP request")),
				mcp.WithString("event_type", mcp.Description("Filter by event type (e.g. payment.completed, auth.login, order.shipped). Use list_log_attributes with field=event_type to discover available types.")),
				mcp.WithString("exception_class", mcp.Description("Filter by exception class name (e.g. NoMethodError, ActiveRecord::RecordNotFound)")),
				mcp.WithString("error_fingerprint", mcp.Description("Filter by error fingerprint to find all occurrences of the same error")),
				mcp.WithString("source_file", mcp.Description("Filter by source file path (e.g. app/models/user.rb). Partial match supported.")),
				mcp.WithString("time_range", mcp.Description("Lookback window: '15m', '1h' (default: all), '6h', '24h', '7d'")),
				mcp.WithNumber("limit", mcp.Description("Maximum entries to return (default: 50, max: 200)")),
				mcp.WithNumber("offset", mcp.Description("Skip this many entries for pagination (default: 0)")),
				mcp.WithString("sort", mcp.Description("Sort order: 'desc' (default, newest first) or 'asc' (oldest first, useful for following causal chains)")),
				mcp.WithString("fields", mcp.Description("Comma-separated list of fields to include (e.g. 'timestamp,level,message'). Omit for all fields. Saves context window on high-volume results.")),
				mcp.WithObject("metadata_filter", mcp.Description("Key-value filter on metadata fields. Exact match: {\"host\": \"server-01\"}, contains: {\"host\": \"~server\"}, key exists: {\"host\": \"*\"}. Prefix ~ for LIKE match, * for existence check. Use list_log_attributes with field=metadata_key to discover available keys.")),
			),
			logSearchHandler(deps.LogStore, deps.ErrorGroupStore),
		)
		b.Add("log_search", "Search log entries by commit hash, environment, request ID, exception class, source file, and more with full-text search", "Log Intelligence", "read", "")

		// Log attribute discovery.
		maybeAddTool(s,
			mcp.NewTool("list_log_attributes",
				mcp.WithDescription("Discover distinct values for log fields. Call this first to learn what services, levels, event types, environments, commit hashes, and metadata keys exist before filtering with log_search. Essential bootstrapping tool for effective log investigation."),
				mcp.WithString("field", mcp.Required(), mcp.Description("Field to list values for: 'service', 'level', 'event_type', 'environment', 'commit_hash', 'request_id', 'exception_class', 'error_fingerprint', 'source_file', or 'metadata_key'")),
				mcp.WithString("time_range", mcp.Description("Lookback window: '15m', '1h', '6h', '24h' (default), '7d'")),
				mcp.WithString("service", mcp.Description("Narrow metadata_key discovery to a specific service")),
			),
			listLogAttributesHandler(deps.LogStore),
		)
		b.Add("list_log_attributes", "Discover distinct values for log fields (services, levels, environments, commit hashes, exception classes, and more)", "Log Intelligence", "read", "")

		// Log context (surrounding entries).
		maybeAddTool(s,
			mcp.NewTool("log_context",
				mcp.WithDescription("Get surrounding log entries around a specific log ID. The 'zoom in' tool — after log_search finds something interesting, use this to see what happened before and after. Optionally filter to the same service for focused investigation."),
				mcp.WithNumber("log_id", mcp.Required(), mcp.Description("Log entry ID (from log_search results)")),
				mcp.WithNumber("before", mcp.Description("Number of entries before the anchor (default: 10, max: 50)")),
				mcp.WithNumber("after", mcp.Description("Number of entries after the anchor (default: 10, max: 50)")),
				mcp.WithBoolean("same_service", mcp.Description("Only show entries from the same service as the anchor (default: false)")),
			),
			logContextHandler(deps.LogStore),
		)
		b.Add("log_context", "Get surrounding log entries around a specific log ID for focused investigation", "Log Intelligence", "read", "")

		// Debugging overview — one call gives Claude the full picture.
		maybeAddTool(s,
			mcp.NewTool("log_summary",
				mcp.WithDescription("Get a debugging-oriented overview: error rates, active deployments (by commit hash), top unique errors with source file:line locations, and slowest endpoints. Use this as the FIRST tool call when investigating issues — it gives the full picture in one call."),
				mcp.WithString("time_range", mcp.Description("Lookback window: '15m', '1h' (default), '6h', '24h', '7d'")),
				mcp.WithString("service", mcp.Description("Filter to a specific service name")),
				mcp.WithString("environment", mcp.Description("Filter by deployment environment (e.g. production, staging)")),
				mcp.WithString("commit_hash", mcp.Description("Filter to a specific deployment by commit hash")),
			),
			logSummaryHandler(deps.LogStore, deps.ErrorGroupStore),
		)
		b.Add("log_summary", "Debugging overview: error rates, active deploys, top errors with source locations, slowest endpoints", "Log Intelligence", "read", "")
	}

	// Error tracking (read-only).
	if deps.ErrorGroupStore != nil {
		maybeAddTool(s,
			mcp.NewTool("error_groups",
				mcp.WithDescription("List error groups aggregated by fingerprint — shows unique errors with occurrence counts, status, and affected services. Use to get a Sentry-like overview of application errors. Errors are automatically grouped by the error_fingerprint field from log entries."),
				mcp.WithString("status", mcp.Description("Filter by status: unresolved (default when omitted), resolved, ignored")),
				mcp.WithString("service", mcp.Description("Filter by service name")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
				mcp.WithString("sort_by", mcp.Description("Sort by: last_seen_at (default), occurrence_count, first_seen_at")),
				mcp.WithNumber("limit", mcp.Description("Number of results (default: 20, max: 100)")),
			),
			errorGroupsHandler(deps.ErrorGroupStore),
		)
		b.Add("error_groups", "List error groups by fingerprint with occurrence counts and status", "Errors", "read", "")

		maybeAddTool(s,
			mcp.NewTool("error_detail",
				mcp.WithDescription("Get full details for a specific error group: exception class, source location, occurrence count, lifecycle events (resolved/reopened history), and recent log entries. Use after error_groups to investigate a specific error."),
				mcp.WithString("fingerprint", mcp.Required(), mcp.Description("Error fingerprint (from error_groups)")),
			),
			errorDetailHandler(deps.ErrorGroupStore, deps.LogStore),
		)
		b.Add("error_detail", "Get full details for a specific error group including history and recent occurrences", "Errors", "read", "")
	}

	// Deep-dive error investigation (one-call context assembly).
	if deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("investigate_error",
				mcp.WithDescription("Deep-dive into a single error: returns exception details (class, message, backtrace, cause chain), request params, SQL queries, surrounding logs, trace timeline, and error group status — ALL in one call. Use when you find a 500 error in log_search and need to understand why it failed. Accepts either a log_id or trace_id as entry point."),
				mcp.WithNumber("log_id", mcp.Description("Log entry ID (from log_search results). Provide this OR trace_id.")),
				mcp.WithString("trace_id", mcp.Description("Trace/correlation ID. If provided without log_id, finds the first error entry in the trace.")),
			),
			investigateErrorHandler(deps.LogStore, deps.ErrorGroupStore),
		)
		b.Add("investigate_error", "Deep-dive into an error: exception, backtrace, params, SQL, context, error group in one call", "Errors", "read", "")
	}

	// Uptime / Health Check monitoring (read-only).
	if deps.HealthCheckStore != nil {
		maybeAddTool(s,
			mcp.NewTool("list_healthchecks",
				mcp.WithDescription("List all configured HTTP health checks with their current status, response times, and configuration. Use to see what endpoints are being monitored."),
			),
			listHealthchecksHandler(deps.HealthCheckStore),
		)
		b.Add("list_healthchecks", "List all health checks with current status and configuration", "Uptime", "read", "")

		maybeAddTool(s,
			mcp.NewTool("uptime_status",
				mcp.WithDescription("Get uptime summary across all health checks: which endpoints are up/down, response times, and uptime percentage. Specify a time window in hours (default 24h, max 720h/30d)."),
				mcp.WithNumber("hours", mcp.Description("Time window in hours (default: 24, max: 720)")),
			),
			uptimeStatusHandler(deps.HealthCheckStore),
		)
		b.Add("uptime_status", "Uptime summary: up/down status, response times, uptime % over a time window", "Uptime", "read", "")
	}

	// Diagnose meta-tool (read-only, aggregates multiple data sources).
	{
		maybeAddTool(s,
			mcp.NewTool("diagnose",
				mcp.WithDescription("All-in-one investigation tool. Returns error summary, log volume, request performance, watch alerts, and health check status in a single call. Saves 4-5 round trips compared to calling individual tools. Start here when investigating an issue."),
				mcp.WithString("service", mcp.Description("Filter to a specific service name")),
				mcp.WithString("timeframe", mcp.Description("Time window: 30m, 1h (default), 6h, 24h, 7d")),
			),
			diagnoseHandler(diagnoseDeps{
				logStore:         deps.LogStore,
				errorGroupStore:  deps.ErrorGroupStore,
				healthCheckStore: deps.HealthCheckStore,
				watchStore:       deps.WatchStore,
			}),
		)
		b.Add("diagnose", "All-in-one investigation: errors, logs, performance, watches, health checks", "Overview", "read", "")
	}

	// Settings (read-only).
	if deps.SettingsStore != nil {
		maybeAddTool(s,
			mcp.NewTool("get_settings",
				mcp.WithDescription("Get current OpenTrace settings: data retention period and auto-update flag. Does not expose API keys for security."),
			),
			getSettingsHandler(deps.SettingsStore),
		)
		b.Add("get_settings", "Get current OpenTrace settings (retention, auto-update)", "Settings", "read", "")
	}

	// Agent notes (read-only).
	if deps.AgentNoteStore != nil {
		maybeAddTool(s,
			mcp.NewTool("get_notes",
				mcp.WithDescription("Read agent notes — persistent memory attached to entities (services, queries, endpoints, errors, health checks). Use at the start of a session to recall context from previous sessions. Filter by entity_type, or provide both entity_type and entity_id for a specific note."),
				mcp.WithString("entity_type", mcp.Description("Filter by type: query, endpoint, service, healthcheck, error")),
				mcp.WithString("entity_id", mcp.Description("Specific entity ID (requires entity_type)")),
			),
			getNotesHandler(deps.AgentNoteStore),
		)
		b.Add("get_notes", "Read persistent agent notes for entities", "Agent Memory", "read", "")
	}

	// Incident timeline.
	{
		maybeAddTool(s,
			mcp.NewTool("incident_timeline",
				mcp.WithDescription("Build a chronological incident timeline merging error logs, error group lifecycle events (resolved/reopened), watch alerts, and healthcheck status changes. Use when investigating 'what happened between X and Y?', post-incident reviews, or understanding the sequence of events during an outage."),
				mcp.WithString("start", mcp.Required(), mcp.Description("Start time in ISO 8601 format (e.g. 2024-01-15T10:00:00Z)")),
				mcp.WithString("end", mcp.Required(), mcp.Description("End time in ISO 8601 format")),
				mcp.WithString("service", mcp.Description("Filter to a specific service name")),
			),
			incidentTimelineHandler(timelineDeps{
				logStore:         deps.LogStore,
				errorGroupStore:  deps.ErrorGroupStore,
				watchStore:       deps.WatchStore,
				healthCheckStore: deps.HealthCheckStore,
			}),
		)
		b.Add("incident_timeline", "Chronological incident timeline from errors, alerts, healthchecks, and deploys", "Incidents", "read", "")
	}

	// Vacuum/maintenance report.
	if deps.Registry != nil {
		maybeAddTool(s,
			mcp.NewTool("vacuum_report",
				mcp.WithDescription("Generate a PostgreSQL vacuum and maintenance report: dead tuples, last vacuum timestamps, table sizes, and bloat. Flags tables needing VACUUM ANALYZE with recommendations."),
			),
			vacuumReportHandler(deps.Registry),
		)
		b.Add("vacuum_report", "Generate a vacuum/maintenance report with dead tuple stats and recommendations", "Database Introspection", "read", "database connector")

		// Schema overview.
		maybeAddTool(s,
			mcp.NewTool("schema_overview",
				mcp.WithDescription("Get a compact overview of the database schema: tables, columns, types, indexes, and foreign keys. Use to understand the data model before writing queries or creating watchers."),
				mcp.WithString("schema", mcp.Description("Schema name (default: public)")),
				mcp.WithString("table", mcp.Description("Get detailed info for a specific table (columns, indexes, foreign keys). Omit for an overview of all tables.")),
			),
			schemaOverviewHandler(deps.Registry),
		)
		b.Add("schema_overview", "Get database schema overview: tables, columns, indexes, and foreign keys", "Database Introspection", "read", "database connector")

		// PostgreSQL configuration audit.
		maybeAddTool(s,
			mcp.NewTool("pg_config_check",
				mcp.WithDescription("Audit PostgreSQL configuration: key settings (shared_buffers, work_mem, autovacuum, etc.), server info, and warnings for common misconfigurations. Use when diagnosing performance issues or setting up a new database."),
			),
			pgConfigCheckHandler(deps.Registry),
		)
		b.Add("pg_config_check", "Audit PostgreSQL configuration with warnings for common misconfigurations", "Database Introspection", "read", "database connector")

		// Disk usage breakdown.
		maybeAddTool(s,
			mcp.NewTool("disk_usage",
				mcp.WithDescription("Show detailed disk usage breakdown: database total size, per-table sizes (table, index, TOAST). Use when investigating disk pressure or planning capacity."),
			),
			diskUsageHandler(deps.Registry),
		)
		b.Add("disk_usage", "Show detailed disk usage breakdown by table, index, and TOAST", "Database Introspection", "read", "database connector")

		// WAL/checkpoint health.
		maybeAddTool(s,
			mcp.NewTool("checkpoint_stats",
				mcp.WithDescription("Show WAL and checkpoint health: checkpoint frequency, buffer writes, WAL generation rate. Use when diagnosing write performance issues or WAL bloat."),
			),
			checkpointStatsHandler(deps.Registry),
		)
		b.Add("checkpoint_stats", "Show WAL and checkpoint health with performance warnings", "Database Introspection", "read", "database connector")

		// Sequence exhaustion risk.
		maybeAddTool(s,
			mcp.NewTool("sequence_health",
				mcp.WithDescription("Check sequence exhaustion risk: current usage percentage, data type, and capacity warnings. Use to prevent integer overflow errors on primary key sequences."),
			),
			sequenceHealthHandler(deps.Registry),
		)
		b.Add("sequence_health", "Check sequence exhaustion risk and capacity warnings", "Database Introspection", "read", "database connector")

		// Table bloat estimation.
		maybeAddTool(s,
			mcp.NewTool("bloat_estimate",
				mcp.WithDescription("Estimate table bloat using dead tuple ratios: reclaimable space per table, VACUUM recommendations. More detailed than vacuum_report for bloat-specific analysis."),
			),
			bloatEstimateHandler(deps.Registry),
		)
		b.Add("bloat_estimate", "Estimate table bloat and reclaimable space with VACUUM recommendations", "Database Introspection", "read", "database connector")

		// Long-running transactions.
		maybeAddTool(s,
			mcp.NewTool("long_transactions",
				mcp.WithDescription("Find long-running and idle-in-transaction sessions with their held locks. Use when db_activity shows stuck queries or when vacuum can't clean dead tuples."),
				mcp.WithNumber("min_duration_seconds", mcp.Description("Minimum transaction duration in seconds (default: 30)")),
			),
			longTransactionsHandler(deps.Registry),
		)
		b.Add("long_transactions", "Find long-running and idle-in-transaction sessions with their locks", "Database Introspection", "read", "database connector")
	}

	// Composite investigation runbooks.
	if deps.Registry != nil {
		maybeAddTool(s,
			mcp.NewTool("runbook",
				mcp.WithDescription("Run a composite investigation playbook that executes multiple diagnostic queries at once. Available playbooks: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike. Use when you need a comprehensive investigation rather than individual tool calls."),
				mcp.WithString("playbook", mcp.Required(), mcp.Description("Playbook to run: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike")),
			),
			runbookHandler(deps.Registry, deps.LogStore),
		)
		b.Add("runbook", "Run a composite investigation playbook (slow_database, connection_exhaustion, etc.)", "Database Introspection", "read", "database connector")
	}

	// System overview — high-level health dashboard.
	ovDeps := overviewDeps{
		logStore:         deps.LogStore,
		dsStore:          deps.DataSourceStore,
		serverStore:      deps.ServerStore,
		errorGroupStore:  deps.ErrorGroupStore,
		watchStore:       deps.WatchStore,
		healthCheckStore: deps.HealthCheckStore,
	}
	maybeAddTool(s, systemOverviewTool(), systemOverviewHandler(ovDeps))
	b.Add("system_overview", "Get high-level system health: errors, alerts, healthchecks, logs, connectors, servers", "Overview", "read", "")

	// Triage — prioritized inbox of items needing attention.
	maybeAddTool(s, triageAlertsTool(), triageAlertsHandler(ovDeps))
	b.Add("triage_alerts", "Get prioritized list of items needing attention", "Overview", "read", "")

	// User listing (read-only, admin-gated at the server level).
	if deps.UserStore != nil {
		maybeAddTool(s, listUsersTool(), listUsersHandler(deps.UserStore))
		b.Add("list_users", "List all user accounts with roles and status", "Users", "read", "")
	}

	// Audit log (read-only).
	if deps.AuditStore != nil {
		maybeAddTool(s, getAuditLogTool(), getAuditLogHandler(deps.AuditStore))
		b.Add("get_audit_log", "View recent admin actions for security review", "Audit", "read", "")
	}

	// Trends Dashboard tools (read-only).
	if deps.TrendStore != nil {
		maybeAddTool(s,
			mcp.NewTool("trends",
				mcp.WithDescription("Query time-series metric trends. Returns bucketed data points for charting error rate, response time, request volume, SQL counts, etc. Supports period-over-period comparison and deploy markers."),
				mcp.WithString("metric", mcp.Description("Metric to query: error_rate, p95_response, avg_response, request_volume, avg_sql_count, avg_db_time, cache_hit_ratio, error_count (default: request_volume)")),
				mcp.WithString("interval", mcp.Description("Bucket interval: 5m, 15m, 1h (default), 1d")),
				mcp.WithString("since", mcp.Description("Lookback window: '1h', '24h' (default), '7d'")),
				mcp.WithString("service", mcp.Description("Filter to a specific service")),
				mcp.WithString("endpoint", mcp.Description("Filter to a specific endpoint (controller#action)")),
				mcp.WithString("environment", mcp.Description("Filter by environment")),
				mcp.WithString("compare_to", mcp.Description("Compare to baseline: 'previous_period' or 'previous_week'")),
			),
			trendsHandler(deps.TrendStore),
		)
		b.Add("trends", "Query time-series metric trends with period comparison and deploy markers", "Trends", "read", "")

		maybeAddTool(s,
			mcp.NewTool("top_movers",
				mcp.WithDescription("Find services with the biggest metric changes compared to a baseline period. Useful for identifying regressions after deploys."),
				mcp.WithString("metric", mcp.Description("Metric to compare: error_rate, p95_response (default), request_volume, avg_sql_count")),
				mcp.WithString("since", mcp.Description("Current period lookback: '24h' (default), '7d'")),
				mcp.WithString("baseline", mcp.Description("Baseline: 'previous_period' (default), 'previous_week'")),
				mcp.WithNumber("limit", mcp.Description("Number of results (default: 10)")),
			),
			topMoversHandler(deps.TrendStore),
		)
		b.Add("top_movers", "Find services with the biggest metric changes vs baseline", "Trends", "read", "")
	}

	// Web Analytics tools (read-only).
	if deps.AnalyticsStore != nil {
		maybeAddTool(s,
			mcp.NewTool("web_analytics",
				mcp.WithDescription("Get a high-level overview of web traffic: total requests, top endpoints, error rates, status code breakdown, HTTP method distribution."),
				mcp.WithString("service", mcp.Description("Filter by service")),
				mcp.WithString("since", mcp.Description("Lookback window: '1h', '24h' (default), '7d'")),
			),
			webAnalyticsHandler(deps.AnalyticsStore),
		)
		b.Add("web_analytics", "High-level web traffic overview: requests, error rates, status codes", "Analytics", "read", "")

		maybeAddTool(s,
			mcp.NewTool("top_endpoints",
				mcp.WithDescription("Rank endpoints by traffic volume, error rate, or response time. Find busiest, slowest, or most error-prone endpoints."),
				mcp.WithString("service", mcp.Description("Filter by service")),
				mcp.WithString("sort_by", mcp.Description("Sort by: request_count (default), error_rate, avg_duration, p95_duration")),
				mcp.WithString("since", mcp.Description("Lookback window: '24h' (default), '7d'")),
				mcp.WithNumber("min_requests", mcp.Description("Minimum request count to include (default: 5)")),
				mcp.WithNumber("limit", mcp.Description("Number of results (default: 20)")),
			),
			topEndpointsHandler(deps.AnalyticsStore),
		)
		b.Add("top_endpoints", "Rank endpoints by traffic, error rate, or response time", "Analytics", "read", "")

		maybeAddTool(s,
			mcp.NewTool("traffic_heatmap",
				mcp.WithDescription("Get a 24x7 heatmap of traffic volume by day of week and hour. Shows busiest and quietest times for scheduling maintenance."),
				mcp.WithString("service", mcp.Description("Filter by service")),
				mcp.WithString("metric", mcp.Description("Metric: request_count (default), error_count, avg_duration")),
			),
			trafficHeatmapHandler(deps.AnalyticsStore),
		)
		b.Add("traffic_heatmap", "24x7 traffic heatmap by day and hour for capacity planning", "Analytics", "read", "")
	}

	// User Journey + Session Timeline (Phase 2)
	if deps.JourneyStore != nil {
		maybeAddTool(s,
			mcp.NewTool("user_journey",
				mcp.WithDescription("Reconstruct the sequence of HTTP requests a user made in a session. Shows what they did before/after an error or slow request."),
				mcp.WithString("user_id", mcp.Description("Find sessions for this user")),
				mcp.WithString("session_id", mcp.Description("Specific session to examine")),
				mcp.WithString("since", mcp.Description("Time range: 1h, 24h, 7d (default: 24h)")),
				mcp.WithNumber("limit", mcp.Description("Max sessions to return (default: 10)")),
			),
			userJourneyHandler(deps.JourneyStore))
		b.Add("user_journey", "Reconstruct user sessions and request paths for debugging", "Journey", "read", "")

		maybeAddTool(s,
			mcp.NewTool("path_analysis",
				mcp.WithDescription("Analyze common navigation patterns across all users. Find the most frequent paths, drop-off points, and paths that lead to errors."),
				mcp.WithString("service", mcp.Description("Filter by service")),
				mcp.WithString("since", mcp.Description("Time range (default: 7d)")),
				mcp.WithNumber("min_occurrences", mcp.Description("Minimum times a path must appear (default: 5)")),
				mcp.WithNumber("path_length", mcp.Description("Max steps in path (default: 5)")),
				mcp.WithBoolean("error_paths_only", mcp.Description("Only show paths ending in errors")),
				mcp.WithString("starting_from", mcp.Description("Only paths starting with this Controller#Action")),
			),
			pathAnalysisHandler(deps.JourneyStore))
		b.Add("path_analysis", "Discover common user navigation flows and error paths", "Journey", "read", "")

		maybeAddTool(s,
			mcp.NewTool("funnel_analysis",
				mcp.WithDescription("Define and analyze conversion funnels. Track signup flows, checkout processes, onboarding, etc."),
				mcp.WithString("action", mcp.Description("create | analyze | list | delete (default: list)")),
				mcp.WithNumber("funnel_id", mcp.Description("Required for analyze/delete")),
				mcp.WithString("name", mcp.Description("Required for create")),
				mcp.WithString("service", mcp.Description("Optional service filter")),
				mcp.WithString("since", mcp.Description("For analyze — time range (default: 7d)")),
			),
			funnelAnalysisHandler(deps.JourneyStore))
		b.Add("funnel_analysis", "Define and analyze step-by-step conversion funnels", "Journey", "write", "")

		maybeAddTool(s,
			mcp.NewTool("request_timeline",
				mcp.WithDescription("Get a detailed waterfall timeline for a specific request. Shows every SQL query, view render, cache operation, and HTTP call with timing."),
				mcp.WithNumber("log_id", mcp.Description("The log entry ID to analyze"), mcp.Required()),
				mcp.WithNumber("min_duration_ms", mcp.Description("Only show events slower than this (default: 0)")),
			),
			requestTimelineHandler(deps.JourneyStore))
		b.Add("request_timeline", "Waterfall breakdown of a single request's SQL, views, cache, HTTP calls", "Journey", "read", "")

		maybeAddTool(s,
			mcp.NewTool("session_waterfall",
				mcp.WithDescription("Get timelines for all requests in a session. Shows the full user experience sequentially."),
				mcp.WithString("session_id", mcp.Description("The session to examine"), mcp.Required()),
				mcp.WithBoolean("summary_only", mcp.Description("Only return per-request summaries without events (default: false)")),
			),
			sessionWaterfallHandler(deps.JourneyStore))
		b.Add("session_waterfall", "Full session timeline showing all request waterfalls sequentially", "Journey", "read", "")
	}

	// Error Impact (Phase 3 — cohort-based error tracking)
	if deps.ErrorImpactStore != nil {
		maybeAddTool(s,
			mcp.NewTool("error_impact",
				mcp.WithDescription("Get user impact analysis for an error: how many users are affected, common traits (browser, OS), affected user list, and impact score. Use after error_groups or error_detail to understand the blast radius."),
				mcp.WithString("fingerprint", mcp.Required(), mcp.Description("Error fingerprint (from error_groups or error_detail)")),
				mcp.WithNumber("limit", mcp.Description("Max affected users to return (default: 10)")),
			),
			errorImpactHandler(deps.ErrorImpactStore, deps.ErrorGroupStore))
		b.Add("error_impact", "Analyze user impact for an error: affected users, common traits, score", "Errors", "read", "")

		maybeAddTool(s,
			mcp.NewTool("user_errors",
				mcp.WithDescription("List all errors affecting a specific user. Shows every error they've encountered with occurrence counts and status. Use to understand a user's experience or investigate a support ticket."),
				mcp.WithString("user_id", mcp.Required(), mcp.Description("The user ID to look up")),
				mcp.WithString("since", mcp.Description("Lookback window: '1h', '24h' (default), '7d'")),
			),
			userErrorsHandler(deps.ErrorImpactStore))
		b.Add("user_errors", "List all errors affecting a specific user", "Errors", "read", "")

		maybeAddTool(s,
			mcp.NewTool("top_errors_by_impact",
				mcp.WithDescription("Rank errors by user impact — the errors affecting the most users, weighted by recency. Like a prioritized error inbox based on real user pain, not just occurrence count."),
				mcp.WithString("status", mcp.Description("Filter by status: unresolved, resolved, ignored")),
				mcp.WithString("service", mcp.Description("Filter by service")),
				mcp.WithString("sort_by", mcp.Description("Sort by: impact_score (default), unique_users, occurrence_count, last_seen")),
				mcp.WithString("since", mcp.Description("Lookback window: '24h' (default), '7d'")),
				mcp.WithNumber("limit", mcp.Description("Number of results (default: 20)")),
			),
			topErrorsByImpactHandler(deps.ErrorImpactStore))
		b.Add("top_errors_by_impact", "Rank errors by user impact score (affected users × recency)", "Errors", "read", "")
	}

	// Agent-first watch tools (read-only).
	if deps.WatchStore != nil {
		maybeAddTool(s, watchStatusTool(), watchStatusHandler(deps.WatchStore))
		b.Add("watch_status", "List active watches with current values and pending alerts", "Watches", "read", "")

		if deps.LogStore != nil && deps.WatchMetrics != nil {
			maybeAddTool(s, investigateTool(), investigateHandler(deps.WatchStore, deps.LogStore, deps.WatchMetrics))
			b.Add("investigate", "Investigate an alert or collect data about a service", "Watches", "read", "")
		}
	}
}

// addWriteTools registers write/admin tools (connector tools, create_watcher, preview_watcher).
// When s is nil (catalog-only mode), tools are only cataloged via b.
func addWriteTools(s *server.MCPServer, deps Deps, b *CatalogBuilder) {
	// All connector tools (run queries, etc.).
	// Note: dynamic connector tools are not cataloged here — the web handler
	// merges them at request time from s.registry.AllTools().
	for _, t := range deps.Registry.AllTools() {
		maybeAddTool(s, convertTool(t), bridgeHandler(t))
	}

	// Explain query (admin — executes queries).
	maybeAddTool(s,
		mcp.NewTool("explain_query",
			mcp.WithDescription("Run EXPLAIN ANALYZE on a SQL query to show the execution plan, actual vs estimated rows, and timing. Use when investigating slow queries identified by db_query_stats. The query is validated as SELECT-only."),
			mcp.WithString("query", mcp.Required(), mcp.Description("The SQL SELECT query to analyze")),
			mcp.WithString("format", mcp.Description("Output format: 'text' (default) or 'json'")),
			mcp.WithBoolean("analyze", mcp.Description("Actually execute the query for real timing (default: true). Set to false for estimated-only plan.")),
			mcp.WithBoolean("buffers", mcp.Description("Include buffer usage statistics (default: true). Requires analyze=true.")),
		),
		explainQueryHandler(deps.Registry),
	)
	b.Add("explain_query", "Run EXPLAIN ANALYZE on a query and return the execution plan with optimization tips", "Database Introspection", "admin", "database connector")

	// Kill query (admin — cancels or terminates a backend process).
	maybeAddTool(s,
		mcp.NewTool("kill_query",
			mcp.WithDescription("Cancel or terminate a long-running query by PID. Use after db_activity shows problematic queries. By default uses pg_cancel_backend (graceful); set force=true for pg_terminate_backend."),
			mcp.WithNumber("pid", mcp.Required(), mcp.Description("Process ID of the backend to cancel (from db_activity)")),
			mcp.WithBoolean("force", mcp.Description("Use pg_terminate_backend instead of pg_cancel_backend (default: false). Terminate is more aggressive — use only if cancel doesn't work.")),
		),
		killQueryHandler(deps.Registry),
	)
	b.Add("kill_query", "Cancel or terminate a long-running query by PID", "Database Introspection", "admin", "database connector")

	// Connector management (admin).
	if deps.DataSourceStore != nil {
		maybeAddTool(s,
			mcp.NewTool("create_connector",
				mcp.WithDescription("Create a new connector. Supported types: database (PostgreSQL), mysql, redis, turso (libSQL), logs. After creating, use test_connector to verify the connection and activate it."),
				mcp.WithString("name", mcp.Required(), mcp.Description("Display name for the connector")),
				mcp.WithString("type", mcp.Required(), mcp.Description("Connector type: 'database', 'mysql', 'redis', 'turso', or 'logs'")),
				mcp.WithString("connection_string", mcp.Description("Connection string (required for database, mysql, redis, turso). Format: PostgreSQL DSN, MySQL DSN or mysql:// URL, redis:// URL, or libsql:// URL")),
				mcp.WithString("auth_token", mcp.Description("Auth token (required for Turso connectors)")),
			),
			createConnectorHandler(deps.DataSourceStore),
		)
		b.Add("create_connector", "Create a new connector (database, mysql, redis, turso, logs)", "Connectors", "admin", "")

		if deps.Registry != nil && deps.Config != nil {
			maybeAddTool(s,
				mcp.NewTool("test_connector",
					mcp.WithDescription("Test and activate a connector. Creates the connection, verifies connectivity, and registers it for use by all tools. Use after create_connector or to re-test an existing connector."),
					mcp.WithString("connector_id", mcp.Required(), mcp.Description("Connector UUID (from create_connector or list_connectors)")),
				),
				testConnectorHandler(deps.DataSourceStore, deps.Registry, deps.LogStore, deps.Config, deps.SettingsStore),
			)
			b.Add("test_connector", "Test and activate a connector", "Connectors", "admin", "")
		}

		if deps.Registry != nil {
			maybeAddTool(s,
				mcp.NewTool("delete_connector",
					mcp.WithDescription("Delete a connector and disconnect it. This removes the connector from the database and unregisters it from the active registry."),
					mcp.WithString("connector_id", mcp.Required(), mcp.Description("Connector UUID")),
				),
				deleteConnectorHandler(deps.DataSourceStore, deps.Registry),
			)
			b.Add("delete_connector", "Delete and disconnect a connector", "Connectors", "admin", "")

			maybeAddTool(s,
				mcp.NewTool("update_connector",
					mcp.WithDescription("Update a connector's name or connection config. When the connection string changes, the connector is unregistered — use test_connector afterwards to re-establish the connection."),
					mcp.WithString("connector_id", mcp.Required(), mcp.Description("Connector UUID (from list_connectors)")),
					mcp.WithString("name", mcp.Description("New display name")),
					mcp.WithString("connection_string", mcp.Description("New PostgreSQL connection string (triggers re-registration)")),
				),
				updateConnectorHandler(deps.DataSourceStore, deps.Registry),
			)
			b.Add("update_connector", "Update a connector's name or connection config", "Connectors", "admin", "")
		}
	}

	// Update retention settings (admin).
	if deps.SettingsStore != nil {
		maybeAddTool(s,
			mcp.NewTool("update_retention",
				mcp.WithDescription("Update the data retention period. Logs, alerts, and watcher runs older than the specified number of days will be pruned automatically."),
				mcp.WithNumber("retention_days", mcp.Required(), mcp.Description("Number of days to retain data (1-365)")),
			),
			updateRetentionHandler(deps.SettingsStore),
		)
		b.Add("update_retention", "Update data retention period", "Settings", "admin", "")
	}

	// User management (admin).
	if deps.UserStore != nil {
		maybeAddTool(s, updateUserRoleTool(), updateUserRoleHandler(deps.UserStore))
		b.Add("update_user_role", "Change a user's role to admin or member", "Users", "admin", "")

		maybeAddTool(s, toggleUserActiveTool(), toggleUserActiveHandler(deps.UserStore))
		b.Add("toggle_user_active", "Enable or disable a user account", "Users", "admin", "")

		maybeAddTool(s, deleteUserTool(), deleteUserHandler(deps.UserStore))
		b.Add("delete_user", "Permanently delete a user account", "Users", "admin", "")
	}

	// Error management (admin — changes error group status).
	if deps.ErrorGroupStore != nil {
		maybeAddTool(s,
			mcp.NewTool("resolve_error",
				mcp.WithDescription("Mark an error group as resolved with a reason. The error will auto-reopen if it recurs. Use after fixing the underlying issue."),
				mcp.WithString("fingerprint", mcp.Required(), mcp.Description("Error fingerprint (from error_groups)")),
				mcp.WithString("reason", mcp.Required(), mcp.Description("Why this error is resolved (e.g. 'Fixed in PR #42')")),
			),
			resolveErrorHandler(deps.ErrorGroupStore),
		)
		b.Add("resolve_error", "Mark an error group as resolved (auto-reopens on recurrence)", "Errors", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("ignore_error",
				mcp.WithDescription("Permanently ignore an error group. New occurrences are still counted but won't reopen the group. Use for known noise like health check errors."),
				mcp.WithString("fingerprint", mcp.Required(), mcp.Description("Error fingerprint (from error_groups)")),
				mcp.WithString("reason", mcp.Required(), mcp.Description("Why this error should be ignored (e.g. 'Known health check noise')")),
			),
			ignoreErrorHandler(deps.ErrorGroupStore),
		)
		b.Add("ignore_error", "Permanently ignore an error group", "Errors", "admin", "")
	}

	// Uptime / Health Check management (admin — creates/deletes checks).
	if deps.HealthCheckStore != nil {
		maybeAddTool(s,
			mcp.NewTool("create_healthcheck",
				mcp.WithDescription("Create a new HTTP health check that probes an endpoint at regular intervals. The scheduler automatically starts checking once created."),
				mcp.WithString("name", mcp.Required(), mcp.Description("Human-readable name (e.g. 'Production API')")),
				mcp.WithString("url", mcp.Required(), mcp.Description("Full URL to probe (e.g. 'https://api.example.com/health')")),
				mcp.WithString("method", mcp.Description("HTTP method: GET (default) or HEAD")),
				mcp.WithNumber("interval_secs", mcp.Description("Check interval in seconds (default: 60)")),
				mcp.WithNumber("timeout_secs", mcp.Description("Request timeout in seconds (default: 10)")),
				mcp.WithNumber("expected_status", mcp.Description("Expected HTTP status code (default: 200)")),
			),
			createHealthcheckHandler(deps.HealthCheckStore),
		)
		b.Add("create_healthcheck", "Create a new HTTP health check monitor", "Uptime", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("delete_healthcheck",
				mcp.WithDescription("Delete a health check and all its historical results. Use list_healthchecks to find the ID."),
				mcp.WithString("id", mcp.Required(), mcp.Description("Health check ID (from list_healthchecks)")),
			),
			deleteHealthcheckHandler(deps.HealthCheckStore),
		)
		b.Add("delete_healthcheck", "Delete a health check and its results", "Uptime", "admin", "")
	}

	// Agent notes (write).
	if deps.AgentNoteStore != nil {
		maybeAddTool(s,
			mcp.NewTool("add_note",
				mcp.WithDescription("Save a persistent note about an entity (service, query, endpoint, error, health check). Notes are remembered across sessions and auto-included in relevant tool responses. Use to record system-specific context like 'this query is intentionally slow' or 'this service handles auth'."),
				mcp.WithString("entity_type", mcp.Required(), mcp.Description("Entity type: query, endpoint, service, healthcheck, error")),
				mcp.WithString("entity_id", mcp.Required(), mcp.Description("Entity identifier (fingerprint, URL, query hash, service name)")),
				mcp.WithString("note", mcp.Required(), mcp.Description("The note to save")),
			),
			addNoteHandler(deps.AgentNoteStore),
		)
		b.Add("add_note", "Save a persistent note about an entity for future sessions", "Agent Memory", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("delete_note",
				mcp.WithDescription("Remove a previously saved agent note."),
				mcp.WithString("entity_type", mcp.Required(), mcp.Description("Entity type: query, endpoint, service, healthcheck, error")),
				mcp.WithString("entity_id", mcp.Required(), mcp.Description("Entity identifier")),
			),
			deleteNoteHandler(deps.AgentNoteStore),
		)
		b.Add("delete_note", "Remove a saved agent note", "Agent Memory", "admin", "")
	}

	// Investigation session summary (write).
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

	// Agent-first watch tools (write).
	if deps.WatchStore != nil && deps.LogStore != nil && deps.WatchMetrics != nil {
		maybeAddTool(s, watchTool(), watchHandler(deps.WatchStore, deps.LogStore, deps.WatchMetrics))
		b.Add("watch", "Create a metric watch that monitors a service and alerts on threshold breach", "Watches", "admin", "")

		maybeAddTool(s, dismissWatchTool(), dismissWatchHandler(deps.WatchStore))
		b.Add("dismiss_watch", "Stop a watch or dismiss/acknowledge an alert", "Watches", "admin", "")
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
