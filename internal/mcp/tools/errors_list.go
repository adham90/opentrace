package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Typed response structs for errors list
// ---------------------------------------------------------------------------

// ErrorListResponse is the typed response for the errors list action.
type ErrorListResponse struct {
	TotalUnresolved int                 `json:"total_unresolved"`
	Returned        int                 `json:"returned"`
	ErrorGroups     []ErrorGroupSummary `json:"error_groups"`
}

// ErrorGroupSummary is a compact view of an error group for listing.
type ErrorGroupSummary struct {
	Fingerprint     string `json:"fingerprint"`
	Service         string `json:"service"`
	Environment     string `json:"environment,omitempty"`
	ExceptionClass  string `json:"exception_class,omitempty"`
	Message         string `json:"message"`
	Status          string `json:"status"`
	OccurrenceCount int    `json:"occurrence_count"`
	LastSeenAt      string `json:"last_seen_at"`
	FirstSeenAt     string `json:"first_seen_at"`
	ReopenedCount   int    `json:"reopened_count,omitempty"`
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

func ErrorsList(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	params := store.ListErrorGroupParams{
		Limit: 20,
	}
	if v := ArgString(args, "status"); v != "" {
		params.Status = store.ErrorGroupStatus(v)
	}
	params.Service = ArgString(args, "service")
	params.Environment = env
	if v := ArgString(args, "sort_by"); v != "" {
		params.SortBy = v
	}
	params.Limit = ArgInt(args, "limit", 20, 100)

	// since here means "still erroring in this window", not "first appeared in
	// it" — a group from last month that fired a minute ago is exactly what
	// someone asking for the last hour wants to see. Only applied when the
	// caller actually passed a window; the default listing stays unbounded.
	if since, ok := OptionalSinceParam(args); ok {
		params.ActiveSince = &since
	}

	groups, err := deps.ErrorGroupStore.List(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list error groups: %v", err)), nil
	}

	if len(groups) == 0 {
		return EmptyResult("No error groups found matching the criteria.")
	}

	// Count in the same env the rows came from, otherwise the summary total
	// counts groups the caller was not shown and cannot open.
	unresolvedCount, _ := deps.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved, env)

	summaries := make([]ErrorGroupSummary, len(groups))
	for i, g := range groups {
		summaries[i] = ErrorGroupSummary{
			Fingerprint:     g.Fingerprint,
			Service:         g.Service,
			Environment:     g.Environment,
			ExceptionClass:  g.ExceptionClass,
			Message:         g.Message,
			Status:          string(g.Status),
			OccurrenceCount: g.OccurrenceCount,
			LastSeenAt:      g.LastSeenAt.Format(time.RFC3339),
			FirstSeenAt:     g.FirstSeenAt.Format(time.RFC3339),
			ReopenedCount:   g.ReopenedCount,
		}
	}

	resp := &ErrorListResponse{
		TotalUnresolved: unresolvedCount,
		Returned:        len(summaries),
		ErrorGroups:     summaries,
	}

	var suggestions []ToolSuggestion
	if len(summaries) > 0 {
		suggestions = append(suggestions, Suggest("errors", "Investigate the most frequent error", map[string]any{"action": "detail", "fingerprint": summaries[0].Fingerprint}))
	}
	if unresolvedCount > 5 {
		suggestions = append(suggestions, Suggest("overview", "Get full system health overview", map[string]any{"action": "diagnose"}))
	}
	return JSONResult(resp, suggestions...)
}
