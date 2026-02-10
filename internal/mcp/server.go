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
	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/digest"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// Deps holds the dependencies for the MCP server.
type Deps struct {
	Registry      *connector.Registry
	WatcherStore  store.WatcherStore
	AlertStore    store.AlertStore
	ServerStore   store.ServerStore
	MetricStore   store.MetricStore
	UserStore     store.UserStore
	WatcherRunStore store.WatcherRunStore
	LogStore        store.LogStore
	RuleEvaluator   *watcher.RuleEvaluator
	MCPToken        string // OPENTRACE_MCP_TOKEN from environment
	ServerName      string // OPENTRACE_MCP_NAME — custom server name (default: "opentrace")
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
	name := deps.ServerName
	if name == "" {
		name = "opentrace"
	}

	s := server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(false),
	)

	// Determine access level.
	isAdmin := true    // default: full access (backward compat)
	hasAccess := true

	if deps.UserStore != nil && deps.MCPToken != "" {
		ctx := context.Background()
		user, err := deps.UserStore.GetByMCPToken(ctx, deps.MCPToken)
		if err != nil || user == nil {
			// Invalid token — serve with zero tools.
			hasAccess = false
		} else {
			isAdmin = user.Role == store.RoleAdmin
		}
	}

	if !hasAccess {
		// Start server with zero tools — stays alive but useless.
		return server.ServeStdio(s)
	}

	// Read-only tools (available to all authenticated users).
	addReadOnlyTools(s, deps)

	// Write tools (admin only).
	if isAdmin {
		addWriteTools(s, deps)
	}

	return server.ServeStdio(s)
}

// addReadOnlyTools registers read-only tools available to all users.
func addReadOnlyTools(s *server.MCPServer, deps Deps) {
	// Meta-tool for listing available connectors.
	s.AddTool(
		mcp.NewTool("list_connectors",
			mcp.WithDescription("List all active OpenTrace connectors and their tools"),
		),
		listConnectorsHandler(deps.Registry),
	)

	// Monitor list.
	if deps.WatcherStore != nil {
		s.AddTool(
			mcp.NewTool("list_monitors",
				mcp.WithDescription("List all configured monitors with their status"),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
				mcp.WithString("monitor_type", mcp.Description("Filter by monitor type: ai or rule")),
			),
			listMonitorsHandler(deps.WatcherStore),
		)
	}

	// Alert list.
	if deps.AlertStore != nil {
		s.AddTool(
			mcp.NewTool("list_alerts",
				mcp.WithDescription("List recent alerts from watchers"),
				mcp.WithNumber("limit", mcp.Description("Maximum number of alerts to return (default: 10)")),
				mcp.WithBoolean("unread_only", mcp.Description("Only show unread alerts (default: false)")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
			),
			listAlertsHandler(deps.AlertStore),
		)
	}

	// Health digest.
	if deps.AlertStore != nil && deps.WatcherStore != nil && deps.WatcherRunStore != nil {
		s.AddTool(
			mcp.NewTool("get_digest",
				mcp.WithDescription("Get a health digest summarizing database alerts, monitor status, and trends. Use this when the user asks 'what happened overnight?', 'any issues?', 'daily report', or similar. At the start of a session, consider running this proactively to inform the user of any issues."),
				mcp.WithString("period", mcp.Description("Time period: 'last_24h' (default), 'last_12h', 'last_7d', 'today', 'yesterday'")),
				mcp.WithString("environment", mcp.Description("Optional environment filter (e.g. production, staging)")),
			),
			getDigestHandler(deps.AlertStore, deps.WatcherStore, deps.WatcherRunStore),
		)
	}

	// Database introspection tools (Postgres runtime stats).
	s.AddTool(
		mcp.NewTool("db_query_stats",
			mcp.WithDescription("Show top SQL queries from pg_stat_statements — useful for identifying slow or frequent queries to monitor"),
			mcp.WithString("order_by", mcp.Description("Sort by: calls, total_exec_time (default), mean_exec_time, rows, shared_blks_hit, shared_blks_read")),
			mcp.WithNumber("limit", mcp.Description("Number of queries to return (default: 20, max: 100)")),
		),
		queryStatsHandler(deps.Registry, deps.WatcherStore),
	)

	s.AddTool(
		mcp.NewTool("db_table_stats",
			mcp.WithDescription("Show table-level statistics: row counts, dead tuples, sequential vs index scans, cache hit ratios, and vacuum status"),
			mcp.WithString("table_name", mcp.Description("Filter to a specific table name")),
		),
		dbTableStatsHandler(deps.Registry, deps.WatcherStore),
	)

	s.AddTool(
		mcp.NewTool("db_activity",
			mcp.WithDescription("Show current database activity: connection summary, long-running queries (>10s), idle-in-transaction sessions (>1min), and connection utilization"),
		),
		dbActivityHandler(deps.Registry, deps.WatcherStore),
	)

	// Monitor run history.
	if deps.WatcherStore != nil && deps.WatcherRunStore != nil {
		s.AddTool(
			mcp.NewTool("monitor_run_history",
				mcp.WithDescription("Show recent execution history for a monitor: run status, duration, summary, errors, and alert rate. Use when investigating why a monitor is firing too often, missing issues, or showing errors."),
				mcp.WithString("monitor_id", mcp.Required(), mcp.Description("Monitor UUID (from list_monitors)")),
				mcp.WithNumber("limit", mcp.Description("Maximum runs to return (default: 20, max: 100)")),
				mcp.WithString("status_filter", mcp.Description("Filter by run status: 'all' (default), 'completed', 'failed', 'error', 'alerted'")),
			),
			monitorRunHistoryHandler(deps.WatcherStore, deps.WatcherRunStore),
		)
	}

	// Alert details.
	if deps.AlertStore != nil {
		s.AddTool(
			mcp.NewTool("alert_details",
				mcp.WithDescription("Get full details for a specific alert: the triggering monitor configuration, the run that produced it, and correlated alerts from the same time window."),
				mcp.WithString("alert_id", mcp.Required(), mcp.Description("Alert UUID (from list_alerts)")),
				mcp.WithBoolean("include_correlated", mcp.Description("Include other alerts within +/- 5 minutes (default: true)")),
			),
			alertDetailsHandler(deps.AlertStore, deps.WatcherStore, deps.WatcherRunStore),
		)
	}

	// Lock contention (read-only — queries system catalogs).
	s.AddTool(
		mcp.NewTool("db_locks",
			mcp.WithDescription("Show current lock contention: blocking chains, lock types, and waiting queries. Use when db_activity shows long-running or idle-in-transaction sessions, or when users report the database is stuck."),
			mcp.WithBoolean("blocking_only", mcp.Description("Only show lock chains where one query is blocking another (default: true). Set to false to see all held locks.")),
		),
		dbLocksHandler(deps.Registry),
	)

	// Log aggregation and pattern detection.
	if deps.LogStore != nil {
		s.AddTool(
			mcp.NewTool("log_stats",
				mcp.WithDescription("Aggregate log statistics: volume by level/service, error rate trends, and most common error patterns. Use when investigating 'what's going wrong?', 'are errors increasing?', or 'which service has the most issues?'. Unlike log_search which returns individual entries, this returns aggregated counts and patterns."),
				mcp.WithString("time_range", mcp.Description("Lookback window: '15m', '1h' (default), '6h', '24h', '7d'")),
				mcp.WithString("group_by", mcp.Description("Primary grouping: 'level' (default), 'service', 'pattern' (clusters similar error messages)")),
				mcp.WithString("service", mcp.Description("Filter to a specific service name")),
				mcp.WithString("level", mcp.Description("Filter to a specific log level (debug, info, warn, error, fatal)")),
				mcp.WithString("environment", mcp.Description("Filter to a specific environment")),
				mcp.WithString("bucket_interval", mcp.Description("Time bucket size for trend data: '1m', '5m' (default), '15m', '1h'")),
			),
			logStatsHandler(deps.LogStore),
		)
	}

	// Distributed trace lookup.
	if deps.LogStore != nil {
		s.AddTool(
			mcp.NewTool("trace_lookup",
				mcp.WithDescription("Follow a distributed trace across services. Given a trace ID, assembles all log entries from that trace ordered by timestamp, showing the request journey through services, timing between hops, and where errors occurred. Use when investigating a specific request failure or latency issue."),
				mcp.WithString("trace_id", mcp.Required(), mcp.Description("The trace/correlation ID to look up (from log entries or error reports)")),
				mcp.WithBoolean("include_context", mcp.Description("Include surrounding log entries (+/- 2 seconds) from each service for additional context (default: false)")),
			),
			traceLookupHandler(deps.LogStore),
		)
	}

	// Index health analysis (read-only — queries system catalogs).
	s.AddTool(
		mcp.NewTool("db_index_analysis",
			mcp.WithDescription("Analyze database index health: find unused indexes (wasting disk/write overhead), missing indexes (tables with high sequential scan ratios), duplicate indexes, and bloated indexes. Use after db_table_stats shows sequential scans or db_query_stats shows slow queries."),
			mcp.WithString("table_name", mcp.Description("Analyze indexes for a specific table (omit for all tables)")),
			mcp.WithBoolean("include_suggestions", mcp.Description("Include CREATE/DROP INDEX suggestions (default: true)")),
		),
		dbIndexAnalysisHandler(deps.Registry),
	)

	// Period comparison (read-only — uses log/alert stores).
	if deps.LogStore != nil {
		s.AddTool(
			mcp.NewTool("compare_periods",
				mcp.WithDescription("Compare metrics between two time periods to identify what changed. Compares error rates, log volumes, or alert counts between a current period and a baseline. Use when the user asks 'what changed?', 'why is it slow now?', or 'is this worse than yesterday?'."),
				mcp.WithString("metric", mcp.Required(), mcp.Description("What to compare: 'errors' (log error rates), 'log_volume' (total log counts by level), 'alerts' (alert counts by severity)")),
				mcp.WithString("current_period", mcp.Description("Current period: 'last_1h' (default), 'last_6h', 'last_24h', 'today'")),
				mcp.WithString("baseline_period", mcp.Description("Baseline to compare against: 'previous' (default), 'yesterday_same_time', 'last_week_same_time'")),
				mcp.WithString("service", mcp.Description("Filter to a specific service (for error/log_volume metrics)")),
				mcp.WithString("environment", mcp.Description("Filter to a specific environment")),
			),
			comparePeriodsHandler(deps.LogStore, deps.AlertStore),
		)
	}

	// Server metrics read tools.
	if deps.ServerStore != nil && deps.MetricStore != nil {
		s.AddTool(
			mcp.NewTool("list_servers",
				mcp.WithDescription("List all monitored servers with their status (online/offline/unknown)"),
			),
			listServersHandler(deps.ServerStore),
		)

		s.AddTool(
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

		s.AddTool(
			mcp.NewTool("server_health",
				mcp.WithDescription("Get current health snapshot for a server — latest value for every metric"),
				mcp.WithString("server_id", mcp.Required(), mcp.Description("Server UUID (from list_servers)")),
			),
			serverHealthHandler(deps.ServerStore, deps.MetricStore),
		)
	}

	// Connection pool stats (read-only — queries pg_stat_activity).
	s.AddTool(
		mcp.NewTool("connection_pool_stats",
			mcp.WithDescription("Show connection pool health: current utilization, idle/active connections, wait queue depth, and per-application breakdown. Use when diagnosing 'database is slow' or 'connection timeout' issues."),
		),
		connectionPoolStatsHandler(deps.Registry),
	)
}

// addWriteTools registers write/admin tools (connector tools, create_monitor, preview_monitor).
func addWriteTools(s *server.MCPServer, deps Deps) {
	// All connector tools (run queries, etc.).
	for _, t := range deps.Registry.AllTools() {
		s.AddTool(convertTool(t), bridgeHandler(t))
	}

	// Explain query (admin — executes queries).
	s.AddTool(
		mcp.NewTool("explain_query",
			mcp.WithDescription("Run EXPLAIN ANALYZE on a SQL query to show the execution plan, actual vs estimated rows, and timing. Use when investigating slow queries identified by db_query_stats. The query is validated as SELECT-only."),
			mcp.WithString("query", mcp.Required(), mcp.Description("The SQL SELECT query to analyze")),
			mcp.WithString("format", mcp.Description("Output format: 'text' (default) or 'json'")),
			mcp.WithBoolean("analyze", mcp.Description("Actually execute the query for real timing (default: true). Set to false for estimated-only plan.")),
			mcp.WithBoolean("buffers", mcp.Description("Include buffer usage statistics (default: true). Requires analyze=true.")),
		),
		explainQueryHandler(deps.Registry),
	)

	// Create monitor.
	if deps.WatcherStore != nil {
		s.AddTool(
			mcp.NewTool("create_monitor",
				mcp.WithDescription(`Create a new monitor. Use monitor_type=ai for AI-powered analysis or monitor_type=rule for threshold-based checks.

Natural language examples for rule monitors:
- "Alert when active connections exceed 80" → rule_config: {"source":"query","query":"SELECT count(*) FROM pg_stat_activity WHERE state='active'","metric":"value","operator":"gt","threshold":80}
- "Alert when dead tuples on users table exceed 10000" → rule_config: {"source":"query","query":"SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname='users'","metric":"value","operator":"gt","threshold":10000}
- "Alert when slow queries exceed 5 seconds average" → rule_config: {"source":"query","query":"SELECT mean_exec_time FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 1","metric":"value","operator":"gt","threshold":5000}

Use db_query_stats, db_activity, and db_table_stats to discover what to monitor, then use this tool to create the monitor.`),
				mcp.WithString("title", mcp.Required(), mcp.Description("Title for the monitor")),
				mcp.WithString("monitor_type", mcp.Description("Monitor type: ai (default) or rule")),
				mcp.WithString("description", mcp.Description("Instructions for the AI agent (required for ai monitors)")),
				mcp.WithString("rule_config", mcp.Description("JSON object for rule monitors: {source, query, metric, operator, threshold, filter, checks, latency_threshold_ms}")),
				mcp.WithString("data_source_id", mcp.Description("Data source ID for query/health rule monitors")),
				mcp.WithString("service", mcp.Description("Filter by service name (ai monitors)")),
				mcp.WithString("level", mcp.Description("Filter by log level (ai monitors)")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production)")),
				mcp.WithString("time_range", mcp.Description("Log lookback window (e.g. 5m, 15m, 1h). Also used as run interval if schedule is not set. Default: 15m")),
			mcp.WithString("schedule", mcp.Description("When to run: cron expression (e.g. '0 9 * * 1-5' for weekdays at 9am), interval (e.g. '5m'), or predefined (@hourly, @daily). If omitted, uses time_range as interval.")),
				mcp.WithString("query", mcp.Description("Full-text search query for logs (ai monitors)")),
				mcp.WithString("severity", mcp.Description("Alert severity: info, warning, or critical (default: warning)")),
				mcp.WithString("model", mcp.Description("LLM model name (ai monitors only)")),
				mcp.WithString("effort", mcp.Description("Analysis effort: low, medium (default), or high (ai monitors only)")),
				mcp.WithString("watcher_environment", mcp.Description("Environment to assign to the monitor (e.g. production, staging)")),
			),
			createMonitorHandler(deps.WatcherStore),
		)
	}

	// Preview monitor (rule evaluation without saving).
	if deps.RuleEvaluator != nil {
		s.AddTool(
			mcp.NewTool("preview_monitor",
				mcp.WithDescription(`Run a rule monitor evaluation ad-hoc without saving. Returns the current value and whether it would trigger an alert.

Use this to test a rule_config before creating a monitor. The query in rule_config must be a valid SELECT statement. For example: {"source":"query","query":"SELECT count(*) FROM pg_stat_activity WHERE state='active'","metric":"value","operator":"gt","threshold":80}

Tip: Use db_query_stats, db_activity, or db_table_stats first to understand the database, then preview a monitor rule to verify it works before creating it.`),
				mcp.WithString("rule_config", mcp.Required(), mcp.Description("JSON object: {source, query, metric, operator, threshold, filter, checks, latency_threshold_ms}")),
				mcp.WithString("data_source_id", mcp.Description("Data source ID for query/health rule monitors")),
			),
			previewMonitorHandler(deps.RuleEvaluator),
		)
	}

	// Suggest monitors (admin — suggests creating monitors with ready-to-use configs).
	if deps.WatcherStore != nil || deps.LogStore != nil {
		s.AddTool(
			mcp.NewTool("suggest_monitors",
				mcp.WithDescription(`Analyze the current system state and suggest monitors the user should create.

Returns prioritized suggestions with ready-to-use monitor configurations based on:
- Current error patterns in logs
- Gaps in monitoring coverage (connection pool, replication, disk, security)
- Focus areas: "all" (default), "performance", "errors", "health", "security"

Each suggestion includes a monitor_config that can be passed directly to create_monitor.`),
				mcp.WithString("focus", mcp.Description("Focus area: all, performance, errors, health, security (default: all)")),
			),
			suggestMonitorsHandler(deps.WatcherStore, deps.LogStore),
		)
	}
}

// convertTool maps an agent.Tool to an mcp.Tool with the appropriate
// JSON Schema properties derived from the tool's parameter definitions.
func convertTool(t agent.Tool) mcp.Tool {
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

// bridgeHandler wraps an agent.Tool handler as an MCP ToolHandlerFunc.
// Tool-level errors are returned as MCP error results (not transport errors).
func bridgeHandler(t agent.Tool) server.ToolHandlerFunc {
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

// listConnectorsHandler returns a handler that lists all active connectors
// and their tools.
func listConnectorsHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

// monitorListEntry is a compact representation of a monitor for the list_monitors MCP tool.
type monitorListEntry struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Status               string `json:"status"`
	MonitorType          string `json:"monitor_type"`
	Environment          string `json:"environment,omitempty"`
	Severity             string `json:"severity"`
	TimeRange            string `json:"time_range"`
	Schedule             string `json:"schedule,omitempty"`
	NextRunAt            *time.Time `json:"next_run_at,omitempty"`
	LastRunAt            *time.Time `json:"last_run_at,omitempty"`
	AdaptiveState        string `json:"adaptive_state,omitempty"`
	EffectiveInterval    string `json:"effective_interval,omitempty"`
	ConsecutiveCleanRuns int    `json:"consecutive_clean_runs,omitempty"`
	ConsecutiveErrors    int    `json:"consecutive_errors,omitempty"`
}

// listMonitorsHandler returns a handler that lists all monitors.
func listMonitorsHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		var params store.ListWatcherParams
		if v, ok := args["environment"].(string); ok && v != "" {
			params.Environment = v
		}
		if v, ok := args["monitor_type"].(string); ok && v != "" {
			params.MonitorType = store.MonitorType(v)
		}
		watchers, err := ws.List(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list monitors: %v", err)), nil
		}

		if len(watchers) == 0 {
			return mcp.NewToolResultText("No monitors configured."), nil
		}

		entries := make([]monitorListEntry, 0, len(watchers))
		for _, w := range watchers {
			e := monitorListEntry{
				ID:          w.ID.String(),
				Title:       w.Title,
				Status:      string(w.Status),
				MonitorType: string(w.MonitorType),
				Environment: w.Environment,
				Severity:    string(w.Severity),
				TimeRange:   w.TimeRange,
				Schedule:    w.Schedule,
				NextRunAt:   w.NextRunAt,
				LastRunAt:   w.LastRunAt,
			}

			// Include adaptive info if not in default normal state
			if w.AdaptiveConfig != nil && w.AdaptiveConfig.Enabled {
				e.AdaptiveState = string(w.AdaptiveState)
				e.ConsecutiveCleanRuns = w.ConsecutiveCleanRuns
				e.ConsecutiveErrors = w.ConsecutiveErrors
				e.EffectiveInterval = effectiveInterval(w)
			}

			entries = append(entries, e)
		}

		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal monitors: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// effectiveInterval returns the current polling interval based on adaptive state.
func effectiveInterval(w store.Watcher) string {
	if w.AdaptiveConfig == nil || !w.AdaptiveConfig.Enabled {
		if w.Schedule != "" {
			return w.Schedule
		}
		return w.TimeRange
	}

	switch w.AdaptiveState {
	case store.AdaptiveEscalated:
		if w.AdaptiveConfig.EscalatedInterval != "" {
			return w.AdaptiveConfig.EscalatedInterval
		}
		return w.TimeRange // already adapted by engine
	case store.AdaptiveRelaxed:
		if w.AdaptiveConfig.RelaxedInterval != "" {
			return w.AdaptiveConfig.RelaxedInterval
		}
		return w.TimeRange
	default:
		if w.Schedule != "" {
			return w.Schedule
		}
		return w.TimeRange
	}
}

// createMonitorHandler returns a handler that creates a new monitor (AI or rule).
func createMonitorHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		title, _ := args["title"].(string)
		if title == "" {
			return mcp.NewToolResultError("title is required"), nil
		}

		monitorType := store.MonitorTypeAI
		if v, ok := args["monitor_type"].(string); ok && v != "" {
			monitorType = store.MonitorType(v)
		}

		description, _ := args["description"].(string)
		if monitorType == store.MonitorTypeAI && description == "" {
			return mcp.NewToolResultError("description is required for ai monitors"), nil
		}

		// Build filters from individual params (for AI monitors).
		filters := make(map[string]string)
		if v, ok := args["service"].(string); ok && v != "" {
			filters["service"] = v
		}
		if v, ok := args["level"].(string); ok && v != "" {
			filters["level"] = v
		}
		if v, ok := args["environment"].(string); ok && v != "" {
			filters["environment"] = v
		}
		if v, ok := args["query"].(string); ok && v != "" {
			filters["query"] = v
		}
		filtersJSON, _ := json.Marshal(filters)

		timeRange := "15m"
		if v, ok := args["time_range"].(string); ok && v != "" {
			timeRange = v
		}

		schedule := ""
		if v, ok := args["schedule"].(string); ok && v != "" {
			if _, err := watcher.ParseSchedule(v); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid schedule expression: %v", err)), nil
			}
			schedule = v
		}

		severity := store.SeverityWarning
		if v, ok := args["severity"].(string); ok && v != "" {
			severity = store.WatcherSeverity(v)
		}

		model, _ := args["model"].(string)

		effort := store.EffortMedium
		if v, ok := args["effort"].(string); ok && v != "" {
			effort = store.WatcherEffort(v)
		}

		watcherEnv, _ := args["watcher_environment"].(string)

		params := store.CreateWatcherParams{
			Title:       title,
			Description: description,
			Environment: watcherEnv,
			MonitorType: monitorType,
			Severity:    severity,
			Filters:     filtersJSON,
			TimeRange:   timeRange,
			Schedule:    schedule,
			Model:       model,
			Effort:      effort,
			Notify:      json.RawMessage(`["dashboard"]`),
		}

		// Parse rule_config JSON string for rule monitors.
		if monitorType == store.MonitorTypeRule {
			rcStr, _ := args["rule_config"].(string)
			if rcStr == "" {
				return mcp.NewToolResultError("rule_config is required for rule monitors"), nil
			}
			var rc store.RuleConfig
			if err := json.Unmarshal([]byte(rcStr), &rc); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid rule_config JSON: %v", err)), nil
			}
			params.RuleConfig = &rc
		}

		if v, ok := args["data_source_id"].(string); ok && v != "" {
			params.DataSourceID = &v
		}

		monitor, err := ws.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create monitor: %v", err)), nil
		}

		data, err := json.MarshalIndent(monitor, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal monitor: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Monitor created successfully:\n%s", string(data))), nil
	}
}

// previewMonitorHandler returns a handler that runs a rule evaluation ad-hoc.
func previewMonitorHandler(re *watcher.RuleEvaluator) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		rcStr, _ := args["rule_config"].(string)
		if rcStr == "" {
			return mcp.NewToolResultError("rule_config is required"), nil
		}

		var rc store.RuleConfig
		if err := json.Unmarshal([]byte(rcStr), &rc); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid rule_config JSON: %v", err)), nil
		}

		tempWatcher := store.Watcher{
			MonitorType: store.MonitorTypeRule,
			RuleConfig:  &rc,
		}
		if v, ok := args["data_source_id"].(string); ok && v != "" {
			tempWatcher.DataSourceID = &v
		}

		start := time.Now()
		result, err := re.Evaluate(ctx, tempWatcher)
		elapsed := time.Since(start).Milliseconds()

		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Preview error: %v (took %dms)", err, elapsed)), nil
		}

		resp := map[string]any{
			"would_alert":   result.HasAlert,
			"summary":       result.Summary,
			"query_time_ms": elapsed,
			"threshold":     rc.Threshold,
			"operator":      rc.Operator,
		}
		if result.Value != nil {
			resp["current_value"] = *result.Value
		}

		// Extract query result sample from details if available.
		if details, ok := result.Details.(map[string]any); ok {
			sample := make(map[string]any)
			for _, key := range []string{"query", "metric", "value", "triggered"} {
				if v, exists := details[key]; exists {
					sample[key] = v
				}
			}
			if len(sample) > 0 {
				resp["query_result_sample"] = sample
			}
		}

		// Add recommendation based on result.
		if result.HasAlert {
			resp["recommendation"] = fmt.Sprintf("This rule WOULD trigger an alert. Current value (%.2f) exceeds threshold (%.2f). Consider adjusting the threshold or creating this monitor.", *result.Value, rc.Threshold)
		} else {
			if result.Value != nil {
				resp["recommendation"] = fmt.Sprintf("This rule would NOT trigger. Current value (%.2f) is within threshold (%.2f). The rule is correctly configured for normal conditions.", *result.Value, rc.Threshold)
			} else {
				resp["recommendation"] = "This rule would NOT trigger. The rule is correctly configured for normal conditions."
			}
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// listServersHandler returns a handler that lists all monitored servers.
func listServersHandler(ss store.ServerStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		servers, err := ss.List(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list servers: %v", err)), nil
		}
		if len(servers) == 0 {
			return mcp.NewToolResultText("No monitored servers."), nil
		}
		data, err := json.MarshalIndent(servers, "", "  ")
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

		data, err := json.MarshalIndent(points, "", "  ")
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
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// listAlertsHandler returns a handler that lists recent alerts.
func listAlertsHandler(as store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		limit := 10
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		unreadOnly := false
		if v, ok := args["unread_only"].(bool); ok {
			unreadOnly = v
		}

		var environment string
		if v, ok := args["environment"].(string); ok {
			environment = v
		}

		alerts, err := as.List(ctx, store.ListAlertParams{
			UnreadOnly:  unreadOnly,
			Environment: environment,
			Limit:       limit,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list alerts: %v", err)), nil
		}

		if len(alerts) == 0 {
			return mcp.NewToolResultText("No alerts found."), nil
		}

		data, err := json.MarshalIndent(alerts, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal alerts: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// getDigestHandler returns a handler that generates an on-the-fly health digest.
func getDigestHandler(as store.AlertStore, ws store.WatcherStore, rs store.WatcherRunStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		now := time.Now().UTC()
		periodStart := now.Add(-24 * time.Hour)
		periodEnd := now

		if v, ok := args["period"].(string); ok && v != "" {
			switch v {
			case "last_12h":
				periodStart = now.Add(-12 * time.Hour)
			case "last_7d":
				periodStart = now.Add(-7 * 24 * time.Hour)
			case "today":
				periodStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			case "yesterday":
				y := now.AddDate(0, 0, -1)
				periodStart = time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, time.UTC)
				periodEnd = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			case "last_24h":
				// default, already set
			}
		}

		environment, _ := args["environment"].(string)

		builder := digest.NewBuilder(as, ws, rs)
		d, err := builder.Generate(ctx, digest.DigestOpts{
			PeriodStart: periodStart,
			PeriodEnd:   periodEnd,
			Environment: environment,
			TopN:        10,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to generate digest: %v", err)), nil
		}

		// Build LLM-friendly response
		resp := buildDigestResponse(d)

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal digest: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// buildDigestResponse converts a Digest into an LLM-friendly response map.
func buildDigestResponse(d *digest.Digest) map[string]any {
	summary := fmt.Sprintf("%d new alerts (%d critical, %d warnings, %d info). %d monitors (%d active). %d runs, %d failed.",
		d.AlertSummary.Total,
		d.AlertSummary.Critical,
		d.AlertSummary.Warning,
		d.AlertSummary.Info,
		d.MonitorSummary.Total,
		d.MonitorSummary.Active,
		d.MonitorSummary.RunsInPeriod,
		d.MonitorSummary.FailedRuns,
	)

	alerts := map[string]any{
		"total_new": d.AlertSummary.Total,
		"critical":  d.AlertSummary.Critical,
		"warning":   d.AlertSummary.Warning,
		"info":      d.AlertSummary.Info,
		"unread":    d.AlertSummary.Unread,
	}

	topAlerts := make([]map[string]any, 0, len(d.TopAlerts))
	for _, a := range d.TopAlerts {
		topAlerts = append(topAlerts, map[string]any{
			"monitor":  a.MonitorTitle,
			"severity": a.Severity,
			"summary":  a.Summary,
			"time":     a.CreatedAt.Format(time.RFC3339),
		})
	}
	alerts["top_alerts"] = topAlerts

	monitors := map[string]any{
		"total":       d.MonitorSummary.Total,
		"active":      d.MonitorSummary.Active,
		"errored":     d.MonitorSummary.InError,
		"failed_runs": d.MonitorSummary.FailedRuns,
	}

	problematic := make([]map[string]any, 0)
	for _, m := range d.MonitorHealth {
		if m.AlertCount > 0 || !m.LastRunOK {
			entry := map[string]any{
				"name":             m.Title,
				"alerts_in_period": m.AlertCount,
			}
			if !m.LastRunOK {
				entry["last_run_failed"] = true
			}
			problematic = append(problematic, entry)
		}
	}
	monitors["problematic"] = problematic

	resp := map[string]any{
		"summary": summary,
		"status":  string(d.Status),
		"period": map[string]string{
			"start": d.PeriodStart.Format(time.RFC3339),
			"end":   d.PeriodEnd.Format(time.RFC3339),
		},
		"alerts":   alerts,
		"monitors": monitors,
	}

	if d.Trends != nil {
		resp["trends"] = map[string]any{
			"alerts_current":       d.Trends.AlertsCurrentCount,
			"alerts_previous":      d.Trends.AlertsPrevCount,
			"alerts_change_pct":    d.Trends.AlertsChangePercent(),
			"failed_runs_current":  d.Trends.FailedRunsCurrent,
			"failed_runs_previous": d.Trends.FailedRunsPrev,
		}
	}

	return resp
}
