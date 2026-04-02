package tools

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

// DatabaseDeps holds all dependencies needed by the database consolidated tool.
type DatabaseDeps struct {
	Registry *connector.Registry
	LogStore store.LogStore
}

// DatabaseHandler returns the handler for the consolidated database tool.
func DatabaseHandler(deps DatabaseDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)

		action := ArgString(args, "action")
		if action == "" {
			return NewToolResultError("action is required. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions"), nil
		}

		switch action {
		case "queries":
			return HandleQueries(ctx, deps, args)
		case "explain":
			return HandleExplain(ctx, deps, args)
		case "tables":
			return HandleTables(ctx, deps, args)
		case "activity":
			return HandleDatabaseActivity(ctx, deps, args)
		case "locks":
			return HandleLocks(ctx, deps, args)
		case "connections":
			return HandleConnections(ctx, deps, args)
		case "indexes":
			return HandleIndexes(ctx, deps, args)
		case "schema":
			return HandleSchema(ctx, deps, args)
		case "storage":
			return HandleStorage(ctx, deps, args)
		case "kill_query":
			return HandleKillQuery(ctx, deps, args)
		case "long_transactions":
			return HandleLongTransactions(ctx, deps, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action %q. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions", action)), nil
		}
	}
}
