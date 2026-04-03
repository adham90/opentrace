package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Action: resolve — resolve error group (from resolveErrorHandler)
// ---------------------------------------------------------------------------

func ErrorsResolve(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	reason := ArgString(args, "reason")
	if reason == "" {
		return NewToolResultError("reason is required (e.g. 'Fixed in PR #42')"), nil
	}

	if err := deps.ErrorGroupStore.Resolve(ctx, fingerprint, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to resolve: %v", err)), nil
	}

	resp := map[string]any{
		"status":      "resolved",
		"fingerprint": fingerprint,
		"reason":      reason,
		"message":     "Error group marked as resolved. It will auto-reopen if the error recurs.",
	}
	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// Action: ignore — ignore error group (from ignoreErrorHandler)
// ---------------------------------------------------------------------------

func ErrorsIgnore(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	reason := ArgString(args, "reason")
	if reason == "" {
		return NewToolResultError("reason is required (e.g. 'Known noise from health checks')"), nil
	}

	if err := deps.ErrorGroupStore.Ignore(ctx, fingerprint, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to ignore: %v", err)), nil
	}

	resp := map[string]any{
		"status":      "ignored",
		"fingerprint": fingerprint,
		"reason":      reason,
		"message":     "Error group permanently ignored. New occurrences will still be counted but won't reopen the group.",
	}
	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// Action: reopen — move an error from ignored/resolved back to unresolved
// ---------------------------------------------------------------------------

func ErrorsReopen(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	reason := ArgString(args, "reason")
	if reason == "" {
		return NewToolResultError("reason is required (e.g. 'Error is still occurring, undo ignore')"), nil
	}

	if err := deps.ErrorGroupStore.Reopen(ctx, fingerprint, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to reopen: %v", err)), nil
	}

	resp := map[string]any{
		"status":      "unresolved",
		"fingerprint": fingerprint,
		"reason":      reason,
		"message":     "Error group reopened. It is now active and will appear in unresolved error lists.",
	}
	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// Action: new — errors first seen within the given time window
// ---------------------------------------------------------------------------

func HandleNewErrors(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	since := GetSinceParam(args, 24*time.Hour)
	service := ArgString(args, "service")

	groups, err := deps.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
		Service: service,
		Since:   &since,
		Limit:   20,
		SortBy:  "first_seen_at",
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list error groups: %v", err)), nil
	}

	type newError struct {
		Fingerprint     string `json:"fingerprint"`
		ExceptionClass  string `json:"exception_class,omitempty"`
		Message         string `json:"message"`
		Service         string `json:"service"`
		OccurrenceCount int    `json:"occurrence_count"`
		FirstSeenAt     string `json:"first_seen_at"`
	}

	newErrors := make([]newError, 0, len(groups))
	for _, g := range groups {
		newErrors = append(newErrors, newError{
			Fingerprint:     g.Fingerprint,
			ExceptionClass:  g.ExceptionClass,
			Message:         Truncate(g.Message, 100),
			Service:         g.Service,
			OccurrenceCount: g.OccurrenceCount,
			FirstSeenAt:     g.FirstSeenAt.Format(time.RFC3339),
		})
	}

	if len(newErrors) == 0 {
		return EmptyResult("No new errors found in the given time window.")
	}

	resp := map[string]any{
		"since":      since.Format(time.RFC3339),
		"count":      len(newErrors),
		"new_errors": newErrors,
	}

	// Suggest investigating the top new error.
	var suggestions []ToolSuggestion
	suggestions = append(suggestions, Suggest("errors", "Investigate the newest error", map[string]any{
		"action":      "detail",
		"fingerprint": newErrors[0].Fingerprint,
	}))
	return JSONResult(resp, suggestions...)
}
