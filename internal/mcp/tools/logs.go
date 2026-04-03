package tools

import (
	"context"

	"github.com/adham90/opentrace/internal/domain/logs"
	"github.com/adham90/opentrace/pkg/store"
)

// LogsDeps holds the dependencies for the consolidated logs tool.
type LogsDeps struct {
	Logs            *logs.Service
	LogStore        store.LogStore       // retained for actions not yet migrated to domain service
	ErrorGroupStore store.ErrorGroupStore
}

// LogsCatalogInfo returns the category, description, and access level for catalog registration.
func LogsCatalogInfo() (category, description, access string) {
	return "Log Intelligence",
		"Unified log intelligence: search, context, attributes, stats, summary, performance, trace, compare",
		"read"
}

// InitLogsDeps ensures the Logs service is constructed from LogStore if not set.
func InitLogsDeps(deps *LogsDeps) {
	if deps.Logs == nil && deps.LogStore != nil {
		deps.Logs = logs.NewService(deps.LogStore)
	}
}

// LogsHandler returns a handler that dispatches to the appropriate action.
func LogsHandler(deps LogsDeps) ToolHandlerFunc {
	InitLogsDeps(&deps)
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)

		action := ArgString(args, "action")
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
