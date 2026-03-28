package tools

import (
	"context"


	"github.com/adham90/opentrace/pkg/store"
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

// LogsHandler returns a handler that dispatches to the appropriate action.
func LogsHandler(deps LogsDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)

		action, _ := args["action"].(string)
		switch action {
		case "search":
			return LogsSearch(ctx, args, deps)
		case "context":
			return LogsContext(ctx, args, deps)
		case "attributes":
			return LogsAttributes(ctx, args, deps)
		case "stats":
			return LogsStats(ctx, args, deps)
		case "summary":
			return LogsSummary(ctx, args, deps)
		case "performance":
			return LogsPerformance(ctx, args, deps)
		case "trace":
			return LogsTrace(ctx, args, deps)
		case "compare":
			return LogsCompare(ctx, args, deps)
		default:
			return NewToolResultError(
				"action is required and must be one of: search, context, attributes, stats, summary, performance, trace, compare",
			), nil
		}
	}
}
