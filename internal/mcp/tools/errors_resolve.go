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

	env, err := resolveWriteEnv(ctx, deps, args, fingerprint)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	if err := deps.ErrorGroupStore.Resolve(ctx, fingerprint, env, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to resolve: %v", err)), nil
	}

	resp := map[string]any{
		"status":      "resolved",
		"fingerprint": fingerprint,
		"environment": envLabel(env),
		"reason":      reason,
		"message":     "Error group marked as resolved. It will auto-reopen if the error recurs.",
	}
	return JSONResult(resp)
}

// resolveWriteEnv determines which environment's row a lifecycle write should
// touch, and refuses the write if the caller may not touch it.
//
// Lifecycle writes are the one place where the read-path convention of "empty
// env means every env" is dangerous: resolving a fingerprint that exists in
// both staging and production would silently close the production incident
// too. So a scoped caller always writes exactly its own env, and an unscoped
// caller (tests, legacy wildcard tokens, the web UI) keeps blanket behaviour.
func resolveWriteEnv(ctx context.Context, deps ErrorsDeps, args map[string]any, fingerprint string) (string, error) {
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return "", err
	}
	if env == "" {
		return "", nil
	}

	// Confirm the group exists in the caller's env before mutating. Without
	// this a resolve against a fingerprint that only lives in another env
	// reports a bare "not found" from the store; here it says which env was
	// searched, which is the difference between a confusing failure and an
	// obvious one.
	if _, err := deps.ErrorGroupStore.Get(ctx, fingerprint, env); err != nil {
		return "", fmt.Errorf("error group %s not found in environment %q", fingerprint, env)
	}
	return env, nil
}

// envLabel describes the env a write landed on for the response body.
func envLabel(env string) string {
	if env == "" {
		return "all"
	}
	return env
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

	env, err := resolveWriteEnv(ctx, deps, args, fingerprint)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	if err := deps.ErrorGroupStore.Ignore(ctx, fingerprint, env, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to ignore: %v", err)), nil
	}

	resp := map[string]any{
		"status":      "ignored",
		"fingerprint": fingerprint,
		"environment": envLabel(env),
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

	env, err := resolveWriteEnv(ctx, deps, args, fingerprint)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	if err := deps.ErrorGroupStore.Reopen(ctx, fingerprint, env, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to reopen: %v", err)), nil
	}

	resp := map[string]any{
		"status":      "unresolved",
		"fingerprint": fingerprint,
		"environment": envLabel(env),
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

	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	groups, err := deps.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
		Service:     service,
		Environment: env,
		Since:       &since,
		Limit:       20,
		SortBy:      "first_seen_at",
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
