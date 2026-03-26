package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/tools"
	"github.com/adham90/opentrace/pkg/store"
)

// addReadOnlyTools registers read-only tools available to all users.
// When s is nil (catalog-only mode), tools are only cataloged via b.
func addReadOnlyTools(s *server.MCPServer, deps Deps, b *CatalogBuilder) {
	// -----------------------------------------------------------------------
	// Consolidated: connectors (read-only actions: list, get)
	// -----------------------------------------------------------------------
	maybeAddTool(s, tools.ConnectorsTool(), tools.ConnectorsHandler(tools.ConnectorsDeps{
		DSStore:       deps.DSStore,
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
		DSStore:          deps.DSStore,
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

// convertTool maps a connector.Tool to an mcp.Tool with the appropriate
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

// bridgeHandler wraps a connector.Tool handler as an MCP ToolHandlerFunc.
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
