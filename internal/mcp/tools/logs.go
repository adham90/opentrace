package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// TraceSessionRecorder is an optional callback for recording trace IDs
// discovered during trace lookups into the current investigation session.
type TraceSessionRecorder func(traceID string)

// LogsDeps holds the dependencies for the consolidated logs tool.
type LogsDeps struct {
	LogStore             store.LogStore
	ErrorGroupStore      store.ErrorGroupStore
	TraceSessionRecorder TraceSessionRecorder // optional, nil-safe
	Ranker               SuggestionRanker     // optional, nil-safe
}

// LogsCatalogInfo returns the category, description, and access level for catalog registration.
func LogsCatalogInfo() (category, description, access string) {
	return "Log Intelligence",
		"Unified log intelligence: search, context, attributes, stats, summary, performance, trace, compare",
		"read"
}

// LogsTool returns the MCP tool definition for the consolidated logs tool.
func LogsTool() mcp.Tool {
	return mcp.NewTool("logs",
		mcp.WithDescription("Unified log intelligence tool. Use 'action' to select the operation: "+
			"search (full-text log search with filters), "+
			"context (surrounding entries around a log ID), "+
			"attributes (discover distinct field values), "+
			"stats (aggregate statistics by level/service/pattern), "+
			"summary (debugging overview: error rates, deploys, top errors, slow endpoints), "+
			"performance (request performance: N+1 queries, slow endpoints), "+
			"trace (distributed trace assembly by trace ID), "+
			"compare (compare metrics between two time periods)."),

		// -- shared parameter --
		mcp.WithString("action", mcp.Required(),
			mcp.Description("Operation to perform: search, context, attributes, stats, summary, performance, trace, compare")),

		// -- search parameters --
		mcp.WithString("query",
			mcp.Description("[search] Full-text search query (searches message content). Also tries matching service names if no FTS results found.")),
		mcp.WithString("service",
			mcp.Description("[search/attributes/stats/summary/compare] Filter by service name")),
		mcp.WithString("level",
			mcp.Description("[search/stats] Filter by log level: debug, info, warn, error, fatal (comma-separated for multiple)")),
		mcp.WithString("environment",
			mcp.Description("[search/summary] Filter by deployment environment (e.g. production, staging, development)")),
		mcp.WithString("commit_hash",
			mcp.Description("[search/summary] Filter by git commit hash. Supports short hashes (prefix match) and full 40-char SHA.")),
		mcp.WithString("trace_id",
			mcp.Description("[search/trace] Filter by trace/correlation ID")),
		mcp.WithString("request_id",
			mcp.Description("[search] Filter by request ID to see all logs from a single HTTP request")),
		mcp.WithString("event_type",
			mcp.Description("[search] Filter by event type (e.g. payment.completed, auth.login)")),
		mcp.WithString("exception_class",
			mcp.Description("[search] Filter by exception class name (e.g. NoMethodError, ActiveRecord::RecordNotFound)")),
		mcp.WithString("error_fingerprint",
			mcp.Description("[search] Filter by error fingerprint to find all occurrences of the same error")),
		mcp.WithString("source_file",
			mcp.Description("[search] Filter by source file path (e.g. app/models/user.rb). Partial match supported.")),
		mcp.WithString("time_range",
			mcp.Description("[search/attributes/stats/summary/performance] Lookback window: '15m', '1h', '6h', '24h', '7d'")),
		mcp.WithNumber("limit",
			mcp.Description("[search/performance] Maximum entries to return (search default: 50 max: 200, performance default: 20 max: 100)")),
		mcp.WithNumber("offset",
			mcp.Description("[search] Skip this many entries for pagination (default: 0)")),
		mcp.WithString("sort",
			mcp.Description("[search] Sort order: 'desc' (default, newest first) or 'asc' (oldest first)")),
		mcp.WithString("fields",
			mcp.Description("[search] Comma-separated list of fields to include (e.g. 'timestamp,level,message'). Omit for all fields.")),
		mcp.WithObject("metadata_filter",
			mcp.Description("[search] Key-value filter on metadata fields. Exact: {\"host\": \"server-01\"}, contains: {\"host\": \"~server\"}, exists: {\"host\": \"*\"}")),

		// -- context parameters --
		mcp.WithNumber("log_id",
			mcp.Description("[context] Log entry ID (from search results)")),
		mcp.WithNumber("before",
			mcp.Description("[context] Number of entries before the anchor (default: 10, max: 50)")),
		mcp.WithNumber("after",
			mcp.Description("[context] Number of entries after the anchor (default: 10, max: 50)")),
		mcp.WithBoolean("same_service",
			mcp.Description("[context] Only show entries from the same service as the anchor (default: false)")),

		// -- attributes parameters --
		mcp.WithString("field",
			mcp.Description("[attributes] Field to list values for: service, level, event_type, environment, commit_hash, request_id, exception_class, error_fingerprint, source_file, metadata_key")),

		// -- stats parameters --
		mcp.WithString("group_by",
			mcp.Description("[stats] Primary grouping: 'level' (default), 'service', 'pattern' (clusters similar error messages)")),
		mcp.WithString("bucket_interval",
			mcp.Description("[stats] Time bucket size for trend data: '1m', '5m' (default), '15m', '1h'")),

		// -- performance parameters --
		mcp.WithString("controller",
			mcp.Description("[performance] Filter by controller name (partial match)")),
		mcp.WithString("path",
			mcp.Description("[performance] Filter by request path (partial match)")),
		mcp.WithBoolean("n_plus_one_only",
			mcp.Description("[performance] Only show requests with N+1 query issues (default: false)")),
		mcp.WithNumber("min_duration_ms",
			mcp.Description("[performance] Minimum request duration in milliseconds")),
		mcp.WithNumber("min_sql_count",
			mcp.Description("[performance] Minimum number of SQL queries in request")),
		mcp.WithString("sort_by",
			mcp.Description("[performance] Sort by: 'duration_ms' (default), 'sql_count', 'db_time_ms', 'duplicate_queries'")),

		// -- trace parameters --
		mcp.WithBoolean("include_context",
			mcp.Description("[trace] Include surrounding log entries (+/- 2 seconds) from each service (default: false)")),

		// -- compare parameters --
		mcp.WithString("metric",
			mcp.Description("[compare] What to compare: 'errors' (log error rates), 'log_volume' (total log counts by level)")),
		mcp.WithString("current_period",
			mcp.Description("[compare] Current period: 'last_1h' (default), 'last_6h', 'last_24h', 'today'")),
		mcp.WithString("baseline_period",
			mcp.Description("[compare] Baseline to compare against: 'previous' (default), 'yesterday_same_time', 'last_week_same_time'")),
	)
}

// LogsHandler returns a handler that dispatches to the appropriate action.
func LogsHandler(deps LogsDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		action, _ := args["action"].(string)
		switch action {
		case "search":
			return logsSearch(ctx, args, deps)
		case "context":
			return logsContext(ctx, args, deps)
		case "attributes":
			return logsAttributes(ctx, args, deps)
		case "stats":
			return logsStats(ctx, args, deps)
		case "summary":
			return logsSummary(ctx, args, deps)
		case "performance":
			return logsPerformance(ctx, args, deps)
		case "trace":
			return logsTrace(ctx, args, deps)
		case "compare":
			return logsCompare(ctx, args, deps)
		default:
			return mcp.NewToolResultError(
				"action is required and must be one of: search, context, attributes, stats, summary, performance, trace, compare",
			), nil
		}
	}
}
