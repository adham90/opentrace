package tools

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/pkg/store"
)

// SuggestionRanker re-ranks tool suggestions based on learned data.
// Provided by the parent mcp package to avoid circular imports.
type SuggestionRanker interface {
	RankAndTrack(suggestions []ToolSuggestion) []ToolSuggestion
}

// withSuggestionsRanked adds a suggested_tools array to a response map.
// If a SuggestionRanker is available, suggestions are re-ranked before adding.
func withSuggestionsRanked(resp map[string]any, ranker SuggestionRanker, suggestions ...ToolSuggestion) {
	if ranker != nil {
		ranked := ranker.RankAndTrack(suggestions)
		if len(ranked) > 0 {
			suggestions = ranked
		}
	}
	WithSuggestions(resp, suggestions...)
}

// SessionInfo provides session identity for recurrence detection.
// Implemented by the parent mcp package's SessionTracker.
type SessionInfo interface {
	CurrentSessionID() string
}

// RecurrenceLinker links errors to investigation sessions and detects recurrence.
// Implemented by the parent mcp package's RecurrenceDetector.
type RecurrenceLinker interface {
	LinkInvestigatedError(ctx context.Context, sessionID, fingerprint string)
	LinkResolvedError(ctx context.Context, sessionID, fingerprint string)
	DetectErrorRecurrence(ctx context.Context, sessionID, fingerprint string, reopenedCount int)
	InjectRecurrenceContext(ctx context.Context, sessionID string, resp map[string]any)
}

// ErrorsDeps holds dependencies for the consolidated errors tool.
type ErrorsDeps struct {
	ErrorGroupStore  store.ErrorGroupStore
	LogStore         store.LogStore
	ErrorImpactStore store.ErrorImpactStore

	// Optional: session tracking and recurrence detection.
	Session    SessionInfo
	Recurrence RecurrenceLinker
	Ranker     SuggestionRanker
}

// ErrorsCatalogInfo returns the category, description, and access level for catalog registration.
func ErrorsCatalogInfo() (category, description, access string) {
	return "Errors",
		"Manage and investigate application errors. Actions: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, new",
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
		case "new":
			return HandleNewErrors(ctx, deps, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %q — valid actions: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, new", action)), nil
		}
	}
}
