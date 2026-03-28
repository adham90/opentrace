package tools

import (
	"context"
	"fmt"


	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
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
	Registry                 *connector.Registry
	QueryMemoryStore         store.QueryMemoryStore
	LogStore                 store.LogStore                 // for runbook error_spike
	RunbookEffectivenessStore store.RunbookEffectivenessStore // for runbook tracking
	SessionTracking          SessionTracking                // optional
}

// RunbookDeps holds all dependencies needed by the runbook tool.
type RunbookDeps struct {
	Registry                 *connector.Registry
	LogStore                 store.LogStore
	RunbookEffectivenessStore store.RunbookEffectivenessStore
	SessionTracking          SessionTracking // optional
}

// DatabaseHandler returns the handler for the consolidated database tool.
func DatabaseHandler(deps DatabaseDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)

		action, _ := args["action"].(string)
		if action == "" {
			return NewToolResultError("action is required. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions"), nil
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
		case "runbook":
			return handleRunbookAction(ctx, deps, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action %q. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions, runbook", action)), nil
		}
	}
}

// handleRunbookAction delegates to the existing runbook handler via RunbookDeps.
func handleRunbookAction(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	rbDeps := RunbookDeps{
		Registry:                  deps.Registry,
		LogStore:                  deps.LogStore,
		RunbookEffectivenessStore: deps.RunbookEffectivenessStore,
		SessionTracking:           deps.SessionTracking,
	}
	handler := RunbookHandler(rbDeps)
	// Rewrite args: runbook handler expects "playbook" param directly (no "action").
	req := MakeCallToolRequest("database", args)
	return handler(ctx, req)
}
