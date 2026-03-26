package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// ---------------------------------------------------------------------------
// SessionTracking — optional callback interface for session tracking
// ---------------------------------------------------------------------------

// SessionTracking allows the consolidated handlers to record actions
// on the current investigation session without depending on the mcp
// package's unexported sessionTracker variable.
type SessionTracking interface {
	TrackExplainedQuery(fingerprint string)
	TrackKilledQuery(pid string)
	TrackRunbookExecution(playbook string)
}

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

// DatabaseDeps holds all dependencies needed by the database consolidated tool.
type DatabaseDeps struct {
	Registry         *connector.Registry
	QueryMemoryStore store.QueryMemoryStore
	SessionTracking  SessionTracking // optional
}

// RunbookDeps holds all dependencies needed by the runbook tool.
type RunbookDeps struct {
	Registry                 *connector.Registry
	LogStore                 store.LogStore
	RunbookEffectivenessStore store.RunbookEffectivenessStore
	SessionTracking          SessionTracking // optional
}

// ---------------------------------------------------------------------------
// database tool definition
// ---------------------------------------------------------------------------

// DatabaseTool returns the MCP tool definition for the consolidated database tool.
func DatabaseTool() mcp.Tool {
	return mcp.NewTool("database",
		mcp.WithDescription(
			"Consolidated database introspection and management tool. "+
				"Supports multiple actions: queries, explain, tables, activity, locks, "+
				"connections, indexes, schema, storage, kill_query, long_transactions. "+
				"Use the 'action' parameter to select the operation."),
		mcp.WithString("action", mcp.Required(),
			mcp.Description("Action to perform: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions"),
		),
		// queries action params
		mcp.WithString("order_by",
			mcp.Description("(queries) Sort by: calls, total_exec_time (default), mean_exec_time, rows, shared_blks_hit, shared_blks_read"),
		),
		mcp.WithNumber("limit",
			mcp.Description("(queries) Number of queries to return (default: 20, max: 100)"),
		),
		mcp.WithString("filter",
			mcp.Description("(queries) Filter queries containing this text"),
		),
		// explain action params
		mcp.WithString("query",
			mcp.Description("(explain) The SQL SELECT query to analyze"),
		),
		mcp.WithString("format",
			mcp.Description("(explain) Output format: 'text' (default) or 'json'"),
		),
		mcp.WithBoolean("analyze",
			mcp.Description("(explain) Actually execute the query for real timing (default: false). Set to true for EXPLAIN ANALYZE."),
		),
		mcp.WithBoolean("buffers",
			mcp.Description("(explain) Include buffer usage stats (default: true, only with analyze=true)"),
		),
		// tables action params
		mcp.WithString("table_name",
			mcp.Description("(tables/indexes) Filter to a specific table name"),
		),
		// locks action params
		mcp.WithBoolean("blocking_only",
			mcp.Description("(locks) Only show blocking chains (default: true). Set to false to see all held locks."),
		),
		// indexes action params
		mcp.WithBoolean("include_suggestions",
			mcp.Description("(indexes) Include CREATE/DROP INDEX suggestions (default: true)"),
		),
		// schema action params
		mcp.WithString("schema",
			mcp.Description("(schema) Schema name (default: public)"),
		),
		mcp.WithString("table",
			mcp.Description("(schema) Get detailed info for a specific table. Omit for overview of all tables."),
		),
		// kill_query action params
		mcp.WithNumber("pid",
			mcp.Description("(kill_query) Process ID of the backend to cancel"),
		),
		mcp.WithBoolean("force",
			mcp.Description("(kill_query) Use pg_terminate_backend instead of pg_cancel_backend (default: false)"),
		),
		// long_transactions action params
		mcp.WithNumber("min_duration_seconds",
			mcp.Description("(long_transactions) Minimum transaction duration in seconds (default: 30)"),
		),
	)
}

// DatabaseHandler returns the handler for the consolidated database tool.
func DatabaseHandler(deps DatabaseDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		action, _ := args["action"].(string)
		if action == "" {
			return mcp.NewToolResultError("action is required. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions"), nil
		}

		switch action {
		case "queries":
			return handleQueries(ctx, deps, args)
		case "explain":
			return handleExplain(ctx, deps, args)
		case "tables":
			return handleTables(ctx, deps, args)
		case "activity":
			return handleDatabaseActivity(ctx, deps, args)
		case "locks":
			return handleLocks(ctx, deps, args)
		case "connections":
			return handleConnections(ctx, deps, args)
		case "indexes":
			return handleIndexes(ctx, deps, args)
		case "schema":
			return handleSchema(ctx, deps, args)
		case "storage":
			return handleStorage(ctx, deps, args)
		case "kill_query":
			return handleKillQuery(ctx, deps, args)
		case "long_transactions":
			return handleLongTransactions(ctx, deps, args)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action %q. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions", action)), nil
		}
	}
}
