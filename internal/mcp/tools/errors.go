package tools

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/pkg/store"
)

// ErrorsDeps holds dependencies for the consolidated errors tool.
type ErrorsDeps struct {
	ErrorGroupStore  store.ErrorGroupStore
	LogStore         store.LogStore
	ErrorImpactStore store.ErrorImpactStore
}

// ErrorsCatalogInfo returns the category, description, and access level for catalog registration.
func ErrorsCatalogInfo() (category, description, access string) {
	return "Errors",
		"Manage and investigate application errors. Actions: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, reopen, new",
		"read"
}

// ErrorsHandler returns the handler for the consolidated errors tool.
func ErrorsHandler(deps ErrorsDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "list":
			return ErrorsList(ctx, deps, args)
		case "detail":
			return ErrorsDetail(ctx, deps, args)
		case "investigate":
			return ErrorsInvestigate(ctx, deps, args)
		case "impact":
			return ErrorsImpact(ctx, deps, args)
		case "user_errors":
			return ErrorsUserErrors(ctx, deps, args)
		case "ranking":
			return ErrorsRanking(ctx, deps, args)
		case "resolve":
			return ErrorsResolve(ctx, deps, args)
		case "ignore":
			return ErrorsIgnore(ctx, deps, args)
		case "reopen":
			return ErrorsReopen(ctx, deps, args)
		case "new":
			return HandleNewErrors(ctx, deps, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %q — valid actions: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, reopen, new", action)), nil
		}
	}
}
