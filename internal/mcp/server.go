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
	"github.com/adham90/opentrace/internal/config"
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

	// New deps for additional MCP tools.
	DataSourceStore store.DataSourceStore       // connector CRUD
	SettingsStore   store.SettingsStore          // retention/auto-update settings
	Executor        *watcher.Executor            // on-demand watcher execution
	Config          *config.Config               // needed by connector.CreateConnector
}

// NewConfiguredServer creates an MCPServer and registers tools based on the
// access level. When isAdmin is true, both read-only and write tools are
// registered; otherwise only read-only tools are registered.
// This is used by both the stdio transport (Serve) and the SSE transport
// (web server).
func NewConfiguredServer(deps Deps, isAdmin bool) *server.MCPServer {
	name := deps.ServerName
	if name == "" {
		name = "opentrace"
	}

	s := server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(false),
	)

	b := &CatalogBuilder{}
	addReadOnlyTools(s, deps, b)

	if isAdmin {
		addWriteTools(s, deps, b)
	}

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

	if deps.UserStore != nil && deps.MCPToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		user, err := deps.UserStore.GetByMCPToken(ctx, deps.MCPToken)
		cancel()
		if err != nil || user == nil {
			// Invalid token — serve with zero tools.
			hasAccess = false
		} else {
			isAdmin = user.Role == store.RoleAdmin
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

	s := NewConfiguredServer(deps, isAdmin)
	return server.ServeStdio(s)
}

// maybeAddTool registers a tool on the MCP server if s is non-nil.
// When s is nil (catalog-only mode), this is a no-op.
func maybeAddTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	if s != nil {
		s.AddTool(tool, handler)
	}
}

// addReadOnlyTools registers read-only tools available to all users.
// When s is nil (catalog-only mode), tools are only cataloged via b.
func addReadOnlyTools(s *server.MCPServer, deps Deps, b *CatalogBuilder) {
	// Meta-tool for listing available connectors.
	maybeAddTool(s,
		mcp.NewTool("list_connectors",
			mcp.WithDescription("List all active OpenTrace connectors and their tools"),
		),
		listConnectorsHandler(deps.Registry),
	)
	b.Add("list_connectors", "List all active OpenTrace connectors and their tools", "Connectors", "read", "")

	// Watcher list.
	if deps.WatcherStore != nil {
		maybeAddTool(s,
			mcp.NewTool("list_watchers",
				mcp.WithDescription("List all configured watchers with their status"),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
				mcp.WithString("watcher_type", mcp.Description("Filter by watcher type: ai or rule")),
			),
			listWatchersHandler(deps.WatcherStore),
		)
		b.Add("list_watchers", "List all configured watchers with their status", "Watchers", "read", "")
	}

	// Alert list.
	if deps.AlertStore != nil {
		maybeAddTool(s,
			mcp.NewTool("list_alerts",
				mcp.WithDescription("List recent alerts from watchers. Use when the user asks 'are there any alerts?', 'what's wrong?', or to find alert IDs for acknowledge_alert/dismiss_alert."),
				mcp.WithNumber("limit", mcp.Description("Maximum number of alerts to return (default: 10)")),
				mcp.WithBoolean("unread_only", mcp.Description("Only show unread alerts (default: false)")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
			),
			listAlertsHandler(deps.AlertStore),
		)
		b.Add("list_alerts", "List recent alerts from watchers", "Alerts", "read", "")
	}

	// Health digest.
	if deps.AlertStore != nil && deps.WatcherStore != nil && deps.WatcherRunStore != nil {
		maybeAddTool(s,
			mcp.NewTool("get_digest",
				mcp.WithDescription("Get a health digest summarizing database alerts, watcher status, and trends. Use this when the user asks 'what happened overnight?', 'any issues?', 'daily report', or similar. At the start of a session, consider running this proactively to inform the user of any issues."),
				mcp.WithString("period", mcp.Description("Time period: 'last_24h' (default), 'last_12h', 'last_7d', 'today', 'yesterday'")),
				mcp.WithString("environment", mcp.Description("Optional environment filter (e.g. production, staging)")),
			),
			getDigestHandler(deps.AlertStore, deps.WatcherStore, deps.WatcherRunStore),
		)
		b.Add("get_digest", "Get a health digest summarizing alerts, watcher status, and trends", "Alerts", "read", "")
	}

	// Database introspection tools (Postgres runtime stats).
	maybeAddTool(s,
		mcp.NewTool("db_query_stats",
			mcp.WithDescription("Show top SQL queries from pg_stat_statements — useful for identifying slow or frequent queries to monitor"),
			mcp.WithString("order_by", mcp.Description("Sort by: calls, total_exec_time (default), mean_exec_time, rows, shared_blks_hit, shared_blks_read")),
			mcp.WithNumber("limit", mcp.Description("Number of queries to return (default: 20, max: 100)")),
			mcp.WithString("filter", mcp.Description("Filter queries by pattern (case-insensitive substring match on query text)")),
		),
		queryStatsHandler(deps.Registry, deps.WatcherStore),
	)
	b.Add("db_query_stats", "Show top SQL queries from pg_stat_statements", "Database Introspection", "read", "database connector")

	maybeAddTool(s,
		mcp.NewTool("db_table_stats",
			mcp.WithDescription("Show table-level statistics: row counts, dead tuples, sequential vs index scans, cache hit ratios, and vacuum status"),
			mcp.WithString("table_name", mcp.Description("Filter to a specific table name")),
		),
		dbTableStatsHandler(deps.Registry, deps.WatcherStore),
	)
	b.Add("db_table_stats", "Show table-level statistics: row counts, dead tuples, scans, cache hits", "Database Introspection", "read", "database connector")

	maybeAddTool(s,
		mcp.NewTool("db_activity",
			mcp.WithDescription("Show current database activity: connection summary, long-running queries (>10s), idle-in-transaction sessions (>1min), and connection utilization"),
		),
		dbActivityHandler(deps.Registry, deps.WatcherStore),
	)
	b.Add("db_activity", "Show current database activity: connections, long-running queries, idle sessions", "Database Introspection", "read", "database connector")

	// Watcher run history.
	if deps.WatcherStore != nil && deps.WatcherRunStore != nil {
		maybeAddTool(s,
			mcp.NewTool("watcher_run_history",
				mcp.WithDescription("Show recent execution history for a watcher: run status, duration, summary, errors, and alert rate. Use when investigating why a watcher is firing too often, missing issues, or showing errors."),
				mcp.WithString("watcher_id", mcp.Required(), mcp.Description("Watcher UUID (from list_watchers)")),
				mcp.WithNumber("limit", mcp.Description("Maximum runs to return (default: 20, max: 100)")),
				mcp.WithString("status_filter", mcp.Description("Filter by run status: 'all' (default), 'completed', 'failed', 'error', 'alerted'")),
			),
			watcherRunHistoryHandler(deps.WatcherStore, deps.WatcherRunStore),
		)
		b.Add("watcher_run_history", "Show execution history for a specific watcher with outcome trends", "Watchers", "read", "")
	}

	// Alert details.
	if deps.AlertStore != nil {
		maybeAddTool(s,
			mcp.NewTool("alert_details",
				mcp.WithDescription("Get full details for a specific alert: the triggering watcher configuration, the run that produced it, and correlated alerts from the same time window."),
				mcp.WithString("alert_id", mcp.Required(), mcp.Description("Alert UUID (from list_alerts)")),
				mcp.WithBoolean("include_correlated", mcp.Description("Include other alerts within +/- 5 minutes (default: true)")),
			),
			alertDetailsHandler(deps.AlertStore, deps.WatcherStore, deps.WatcherRunStore),
		)
		b.Add("alert_details", "Get full alert details including triggering run, related alerts, and context", "Alerts", "read", "")
	}

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
				mcp.WithString("environment", mcp.Description("Filter to a specific environment")),
				mcp.WithString("bucket_interval", mcp.Description("Time bucket size for trend data: '1m', '5m' (default), '15m', '1h'")),
			),
			logStatsHandler(deps.LogStore),
		)
		b.Add("log_stats", "Aggregate log statistics by level, service, or pattern with trend detection", "Log Intelligence", "read", "")
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
				mcp.WithString("environment", mcp.Description("Filter to a specific environment")),
			),
			comparePeriodsHandler(deps.LogStore, deps.AlertStore),
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
				mcp.WithDescription("Search log entries with full-text search and filters. Returns individual log entries (unlike log_stats which returns aggregated counts). Use when you need to find specific log messages, investigate errors, or look up events by trace ID."),
				mcp.WithString("query", mcp.Description("Full-text search query (searches message content)")),
				mcp.WithString("service", mcp.Description("Filter by service name")),
				mcp.WithString("level", mcp.Description("Filter by log level: debug, info, warn, error, fatal")),
				mcp.WithString("trace_id", mcp.Description("Filter by trace/correlation ID")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
				mcp.WithString("time_range", mcp.Description("Lookback window: '15m', '1h' (default: all), '6h', '24h', '7d'")),
				mcp.WithNumber("limit", mcp.Description("Maximum entries to return (default: 50, max: 200)")),
				mcp.WithNumber("offset", mcp.Description("Skip this many entries for pagination (default: 0)")),
			),
			logSearchHandler(deps.LogStore),
		)
		b.Add("log_search", "Search log entries with full-text search and filters", "Log Intelligence", "read", "")
	}

	// Run ad-hoc read-only SQL queries.
	if deps.Registry != nil {
		maybeAddTool(s,
			mcp.NewTool("run_query",
				mcp.WithDescription("Execute a read-only SQL query against the active database connector. The query must be a SELECT statement (enforced by the read-only guardrail). Use for ad-hoc investigation, data exploration, or verifying hypotheses about the database."),
				mcp.WithString("query", mcp.Required(), mcp.Description("SQL SELECT query to execute")),
				mcp.WithString("format", mcp.Description("Output format: 'table' (default), 'json' (structured), or 'csv'")),
			),
			runQueryHandler(deps.Registry),
		)
		b.Add("run_query", "Execute a read-only SQL query against the active database connector", "Database Introspection", "read", "database connector")
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

	// Incident timeline.
	if deps.AlertStore != nil && deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("incident_timeline",
				mcp.WithDescription("Build a chronological incident timeline for a time window, merging alerts and error logs. Use when investigating 'what happened between X and Y?', post-incident reviews, or understanding the sequence of events during an outage."),
				mcp.WithString("start", mcp.Required(), mcp.Description("Start time in ISO 8601 format (e.g. 2024-01-15T10:00:00Z)")),
				mcp.WithString("end", mcp.Required(), mcp.Description("End time in ISO 8601 format")),
				mcp.WithString("environment", mcp.Description("Filter to a specific environment (e.g. production)")),
			),
			incidentTimelineHandler(deps.AlertStore, deps.LogStore),
		)
		b.Add("incident_timeline", "Build a chronological incident timeline merging alerts and error logs", "Incidents", "read", "")
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
			runbookHandler(deps.Registry, deps.AlertStore, deps.LogStore),
		)
		b.Add("runbook", "Run a composite investigation playbook (slow_database, connection_exhaustion, etc.)", "Database Introspection", "read", "database connector")
	}

	// Anomaly detection (statistical baseline comparison).
	if deps.LogStore != nil || deps.AlertStore != nil {
		maybeAddTool(s,
			mcp.NewTool("anomaly_detect",
				mcp.WithDescription("Detect statistical anomalies by comparing a metric's current value against a baseline of 7 previous periods. Uses z-score analysis to flag deviations. Use when you need to know 'is this normal?' for error rates, log volumes, or alert counts."),
				mcp.WithString("metric", mcp.Required(), mcp.Description("Metric to analyze: error_rate, log_volume, alert_count")),
				mcp.WithString("window", mcp.Description("Time window per sample: '15m', '1h' (default), '6h', '24h'")),
				mcp.WithString("environment", mcp.Description("Filter by environment")),
				mcp.WithString("service", mcp.Description("Filter by service (for error_rate and log_volume)")),
			),
			anomalyDetectHandler(deps.LogStore, deps.AlertStore),
		)
		b.Add("anomaly_detect", "Detect statistical anomalies in error rates, log volumes, or alert counts", "Log Intelligence", "read", "")
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

	// Create watcher.
	if deps.WatcherStore != nil {
		maybeAddTool(s,
			mcp.NewTool("create_watcher",
				mcp.WithDescription(`Create a new watcher. Use watcher_type=ai for AI-powered analysis or watcher_type=rule for threshold-based checks.

Natural language examples for rule watchers:
- "Alert when active connections exceed 80" → rule_config: {"source":"query","query":"SELECT count(*) FROM pg_stat_activity WHERE state='active'","metric":"value","operator":"gt","threshold":80}
- "Alert when dead tuples on users table exceed 10000" → rule_config: {"source":"query","query":"SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname='users'","metric":"value","operator":"gt","threshold":10000}
- "Alert when slow queries exceed 5 seconds average" → rule_config: {"source":"query","query":"SELECT mean_exec_time FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 1","metric":"value","operator":"gt","threshold":5000}

Use db_query_stats, db_activity, and db_table_stats to discover what to monitor, then use this tool to create the watcher.`),
				mcp.WithString("title", mcp.Required(), mcp.Description("Title for the watcher")),
				mcp.WithString("watcher_type", mcp.Description("Watcher type: ai (default) or rule")),
				mcp.WithString("description", mcp.Description("Instructions for the AI agent (required for ai watchers)")),
				mcp.WithString("rule_config", mcp.Description(`JSON string for rule watchers. Example: {"source":"query","query":"SELECT count(*) FROM pg_stat_activity WHERE state='active'","metric":"value","operator":"gt","threshold":80}. Fields: source (query|logs|health), query (SQL SELECT), metric (value|count), operator (gt|lt|gte|lte|eq|neq), threshold (number).`)),
				mcp.WithString("data_source_id", mcp.Description("Data source ID for query/health rule watchers")),
				mcp.WithString("service", mcp.Description("Filter by service name (ai watchers)")),
				mcp.WithString("level", mcp.Description("Filter by log level (ai watchers)")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production)")),
				mcp.WithString("time_range", mcp.Description("Log lookback window (e.g. 5m, 15m, 1h). Also used as run interval if schedule is not set. Default: 15m")),
			mcp.WithString("schedule", mcp.Description("When to run: cron expression (e.g. '0 9 * * 1-5' for weekdays at 9am), interval (e.g. '5m'), or predefined (@hourly, @daily). If omitted, uses time_range as interval.")),
				mcp.WithString("query", mcp.Description("Full-text search query for logs (ai watchers)")),
				mcp.WithString("severity", mcp.Description("Alert severity: info, warning, or critical (default: warning)")),
				mcp.WithString("model", mcp.Description("LLM model name (ai watchers only)")),
				mcp.WithString("effort", mcp.Description("Analysis effort: low, medium (default), or high (ai watchers only)")),
				mcp.WithString("watcher_environment", mcp.Description("Environment to assign to the watcher (e.g. production, staging)")),
			),
			createWatcherHandler(deps.WatcherStore),
		)
		b.Add("create_watcher", "Create a new AI or rule-based watcher with SQL query and schedule", "Watchers", "admin", "")
	}

	// Preview watcher (rule evaluation without saving).
	if deps.RuleEvaluator != nil {
		maybeAddTool(s,
			mcp.NewTool("preview_watcher",
				mcp.WithDescription(`Run a rule watcher evaluation ad-hoc without saving. Returns the current value and whether it would trigger an alert.

Use this to test a rule_config before creating a watcher. The query in rule_config must be a valid SELECT statement. For example: {"source":"query","query":"SELECT count(*) FROM pg_stat_activity WHERE state='active'","metric":"value","operator":"gt","threshold":80}

Tip: Use db_query_stats, db_activity, or db_table_stats first to understand the database, then preview a watcher rule to verify it works before creating it.`),
				mcp.WithString("rule_config", mcp.Required(), mcp.Description("JSON object: {source, query, metric, operator, threshold, filter, checks, latency_threshold_ms}")),
				mcp.WithString("data_source_id", mcp.Description("Data source ID for query/health rule watchers")),
			),
			previewWatcherHandler(deps.RuleEvaluator),
		)
		b.Add("preview_watcher", "Run a rule watcher evaluation ad-hoc without saving", "Watchers", "admin", "")
	}

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

	// Alert management tools (admin).
	if deps.AlertStore != nil {
		maybeAddTool(s,
			mcp.NewTool("acknowledge_alert",
				mcp.WithDescription("Mark a specific alert as read/acknowledged. Use after reviewing an alert via alert_details."),
				mcp.WithString("alert_id", mcp.Required(), mcp.Description("Alert UUID (from list_alerts)")),
			),
			acknowledgeAlertHandler(deps.AlertStore),
		)
		b.Add("acknowledge_alert", "Mark an alert as read/acknowledged", "Alerts", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("acknowledge_all_alerts",
				mcp.WithDescription("Mark all alerts as read/acknowledged. Optionally filter by environment."),
				mcp.WithString("environment", mcp.Description("Only acknowledge alerts in this environment (e.g. production)")),
			),
			acknowledgeAllAlertsHandler(deps.AlertStore),
		)
		b.Add("acknowledge_all_alerts", "Mark all alerts as read", "Alerts", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("dismiss_alert",
				mcp.WithDescription("Dismiss a specific alert (hides it from the default view). Use for false positives or resolved issues."),
				mcp.WithString("alert_id", mcp.Required(), mcp.Description("Alert UUID (from list_alerts)")),
			),
			dismissAlertHandler(deps.AlertStore),
		)
		b.Add("dismiss_alert", "Dismiss an alert (hide from default view)", "Alerts", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("dismiss_all_alerts",
				mcp.WithDescription("Dismiss all alerts. Optionally filter by environment."),
				mcp.WithString("environment", mcp.Description("Only dismiss alerts in this environment (e.g. production)")),
			),
			dismissAllAlertsHandler(deps.AlertStore),
		)
		b.Add("dismiss_all_alerts", "Dismiss all alerts", "Alerts", "admin", "")
	}

	// Watcher lifecycle tools (admin).
	if deps.WatcherStore != nil {
		maybeAddTool(s,
			mcp.NewTool("update_watcher",
				mcp.WithDescription("Update an existing watcher's configuration. Only the fields you provide will be changed."),
				mcp.WithString("watcher_id", mcp.Required(), mcp.Description("Watcher UUID (from list_watchers)")),
				mcp.WithString("title", mcp.Description("New title")),
				mcp.WithString("description", mcp.Description("New description/instructions")),
				mcp.WithString("environment", mcp.Description("New environment assignment")),
				mcp.WithString("severity", mcp.Description("New severity: info, warning, or critical")),
				mcp.WithString("time_range", mcp.Description("New log lookback window (e.g. 5m, 15m, 1h)")),
				mcp.WithString("schedule", mcp.Description("New schedule: cron expression, interval, or @hourly/@daily")),
				mcp.WithString("effort", mcp.Description("New analysis effort: low, medium, high (AI watchers only)")),
				mcp.WithString("rule_config", mcp.Description("New rule configuration JSON (rule watchers only)")),
			),
			updateWatcherHandler(deps.WatcherStore),
		)
		b.Add("update_watcher", "Update a watcher's configuration", "Watchers", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("delete_watcher",
				mcp.WithDescription("Permanently delete a watcher. Historical alerts and run data are preserved. Use pause_watcher instead if you want to temporarily stop it."),
				mcp.WithString("watcher_id", mcp.Required(), mcp.Description("Watcher UUID (from list_watchers)")),
			),
			deleteWatcherHandler(deps.WatcherStore),
		)
		b.Add("delete_watcher", "Permanently delete a watcher (alerts preserved)", "Watchers", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("pause_watcher",
				mcp.WithDescription("Pause a watcher so it stops running until resumed. Use when a watcher is noisy and needs tuning."),
				mcp.WithString("watcher_id", mcp.Required(), mcp.Description("Watcher UUID (from list_watchers)")),
			),
			pauseWatcherHandler(deps.WatcherStore),
		)
		b.Add("pause_watcher", "Pause a watcher (stops running until resumed)", "Watchers", "admin", "")

		maybeAddTool(s,
			mcp.NewTool("resume_watcher",
				mcp.WithDescription("Resume a paused watcher so it starts running again on its schedule."),
				mcp.WithString("watcher_id", mcp.Required(), mcp.Description("Watcher UUID (from list_watchers)")),
			),
			resumeWatcherHandler(deps.WatcherStore),
		)
		b.Add("resume_watcher", "Resume a paused watcher", "Watchers", "admin", "")
	}

	// Suggest watchers (admin — suggests creating watchers with ready-to-use configs).
	if deps.WatcherStore != nil || deps.LogStore != nil {
		maybeAddTool(s,
			mcp.NewTool("suggest_watchers",
				mcp.WithDescription(`Analyze the current system state and suggest watchers the user should create.

Returns prioritized suggestions with ready-to-use watcher configurations based on:
- Current error patterns in logs
- Gaps in monitoring coverage (connection pool, replication, disk, security)
- Focus areas: "all" (default), "performance", "errors", "health", "security"

Each suggestion includes a watcher_config that can be passed directly to create_watcher.`),
				mcp.WithString("focus", mcp.Description("Focus area: all, performance, errors, health, security (default: all)")),
			),
			suggestWatchersHandler(deps.WatcherStore, deps.LogStore),
		)
		b.Add("suggest_watchers", "Analyze system state and suggest watchers to create, with ready-to-use configs", "Watchers", "admin", "")
	}

	// Run watcher now (admin — triggers immediate watcher execution).
	if deps.WatcherStore != nil && deps.Executor != nil {
		maybeAddTool(s,
			mcp.NewTool("run_watcher_now",
				mcp.WithDescription("Trigger an immediate execution of a watcher, bypassing its schedule. The watcher runs asynchronously — this returns immediately with a confirmation. Use when you want to test a watcher or need fresh results now."),
				mcp.WithString("watcher_id", mcp.Required(), mcp.Description("Watcher UUID (from list_watchers)")),
			),
			runWatcherNowHandler(deps.WatcherStore, deps.Executor),
		)
		b.Add("run_watcher_now", "Trigger immediate execution of a watcher", "Watchers", "admin", "")
	}

	// Connector management (admin).
	if deps.DataSourceStore != nil {
		maybeAddTool(s,
			mcp.NewTool("create_connector",
				mcp.WithDescription("Create a new database or log connector. After creating, use test_connector to verify the connection and activate it."),
				mcp.WithString("name", mcp.Required(), mcp.Description("Display name for the connector")),
				mcp.WithString("type", mcp.Required(), mcp.Description("Connector type: 'database' or 'logs'")),
				mcp.WithString("connection_string", mcp.Description("PostgreSQL connection string (required for database type)")),
				mcp.WithString("environment", mcp.Description("Environment label (e.g. production, staging)")),
			),
			createConnectorHandler(deps.DataSourceStore),
		)
		b.Add("create_connector", "Create a new database or log connector", "Connectors", "admin", "")

		if deps.Registry != nil && deps.Config != nil {
			maybeAddTool(s,
				mcp.NewTool("test_connector",
					mcp.WithDescription("Test and activate a connector. Creates the connection, verifies connectivity, and registers it for use by all tools. Use after create_connector or to re-test an existing connector."),
					mcp.WithString("connector_id", mcp.Required(), mcp.Description("Connector UUID (from create_connector or list_connectors)")),
				),
				testConnectorHandler(deps.DataSourceStore, deps.Registry, deps.LogStore, deps.Config),
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

// watcherListEntry is a compact representation of a watcher for the list_watchers MCP tool.
type watcherListEntry struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Status               string `json:"status"`
	WatcherType          string `json:"watcher_type"`
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

// listWatchersHandler returns a handler that lists all watchers.
func listWatchersHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		var params store.ListWatcherParams
		if v, ok := args["environment"].(string); ok && v != "" {
			params.Environment = v
		}
		if v, ok := args["watcher_type"].(string); ok && v != "" {
			params.WatcherType = store.WatcherType(v)
		}
		watchers, err := ws.List(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list watchers: %v. Check that the database is accessible.", err)), nil
		}

		if len(watchers) == 0 {
			return mcp.NewToolResultText("No watchers configured."), nil
		}

		entries := make([]watcherListEntry, 0, len(watchers))
		for _, w := range watchers {
			e := watcherListEntry{
				ID:          w.ID.String(),
				Title:       w.Title,
				Status:      string(w.Status),
				WatcherType: string(w.WatcherType),
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal watchers: %v", err)), nil
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

// createWatcherHandler returns a handler that creates a new watcher (AI or rule).
func createWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		title, _ := args["title"].(string)
		if title == "" {
			return mcp.NewToolResultError("title is required"), nil
		}

		watcherType := store.WatcherTypeAI
		if v, ok := args["watcher_type"].(string); ok && v != "" {
			watcherType = store.WatcherType(v)
		}

		description, _ := args["description"].(string)
		if watcherType == store.WatcherTypeAI && description == "" {
			return mcp.NewToolResultError("description is required for ai watchers"), nil
		}

		// Build filters from individual params (for AI watchers).
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
			WatcherType: watcherType,
			Severity:    severity,
			Filters:     filtersJSON,
			TimeRange:   timeRange,
			Schedule:    schedule,
			Model:       model,
			Effort:      effort,
			Notify:      json.RawMessage(`["dashboard"]`),
		}

		// Parse rule_config JSON string for rule watchers.
		if watcherType == store.WatcherTypeRule {
			rcStr, _ := args["rule_config"].(string)
			if rcStr == "" {
				return mcp.NewToolResultError("rule_config is required for rule watchers"), nil
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

		w, err := ws.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create watcher: %v", err)), nil
		}

		data, err := json.MarshalIndent(w, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Watcher created successfully:\n%s", string(data))), nil
	}
}

// previewWatcherHandler returns a handler that runs a rule evaluation ad-hoc.
func previewWatcherHandler(re *watcher.RuleEvaluator) server.ToolHandlerFunc {
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
			WatcherType: store.WatcherTypeRule,
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
			resp["recommendation"] = fmt.Sprintf("This rule WOULD trigger an alert. Current value (%.2f) exceeds threshold (%.2f). Consider adjusting the threshold or creating this watcher.", *result.Value, rc.Threshold)
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to list alerts: %v. Check that the database is accessible.", err)), nil
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
	summary := fmt.Sprintf("%d new alerts (%d critical, %d warnings, %d info). %d watchers (%d active). %d runs, %d failed.",
		d.AlertSummary.Total,
		d.AlertSummary.Critical,
		d.AlertSummary.Warning,
		d.AlertSummary.Info,
		d.WatcherSummary.Total,
		d.WatcherSummary.Active,
		d.WatcherSummary.RunsInPeriod,
		d.WatcherSummary.FailedRuns,
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
			"watcher":  a.WatcherTitle,
			"severity": a.Severity,
			"summary":  a.Summary,
			"time":     a.CreatedAt.Format(time.RFC3339),
		})
	}
	alerts["top_alerts"] = topAlerts

	watchers := map[string]any{
		"total":       d.WatcherSummary.Total,
		"active":      d.WatcherSummary.Active,
		"errored":     d.WatcherSummary.InError,
		"failed_runs": d.WatcherSummary.FailedRuns,
	}

	problematic := make([]map[string]any, 0)
	for _, m := range d.WatcherHealth {
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
	watchers["problematic"] = problematic

	resp := map[string]any{
		"summary": summary,
		"status":  string(d.Status),
		"period": map[string]string{
			"start": d.PeriodStart.Format(time.RFC3339),
			"end":   d.PeriodEnd.Format(time.RFC3339),
		},
		"alerts":   alerts,
		"watchers": watchers,
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

	// Top recommended actions based on digest findings.
	actions := digestRecommendedActions(d)
	if len(actions) > 0 {
		resp["recommended_actions"] = actions
	}

	return resp
}

// digestRecommendedActions generates prioritized action items based on digest data.
func digestRecommendedActions(d *digest.Digest) []string {
	var actions []string

	if d.AlertSummary.Critical > 0 {
		actions = append(actions, fmt.Sprintf("Investigate %d critical alert(s) immediately — run 'alert_details' on each", d.AlertSummary.Critical))
	}

	if d.WatcherSummary.FailedRuns > 0 {
		actions = append(actions, fmt.Sprintf("Fix %d failed watcher run(s) — run 'watcher_run_history' to see errors", d.WatcherSummary.FailedRuns))
	}

	if d.WatcherSummary.InError > 0 {
		actions = append(actions, fmt.Sprintf("%d watcher(s) in error state — check their configuration and run 'list_watchers'", d.WatcherSummary.InError))
	}

	if d.AlertSummary.Unread > 10 {
		actions = append(actions, fmt.Sprintf("%d unread alerts — review and acknowledge or dismiss as appropriate", d.AlertSummary.Unread))
	}

	if d.Trends != nil && d.Trends.AlertsChangePercent() > 50 {
		actions = append(actions, "Alert volume is trending up — run 'compare_periods' with metric='alerts' for details")
	}

	for _, m := range d.WatcherHealth {
		if m.AlertCount > 5 {
			actions = append(actions, fmt.Sprintf("Watcher '%s' fired %d times — may need threshold adjustment", m.Title, m.AlertCount))
		}
	}

	if len(actions) == 0 {
		actions = append(actions, "No urgent actions needed — system looks healthy")
	}

	return actions
}
