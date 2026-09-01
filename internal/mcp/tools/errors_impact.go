package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

const (
	// maxRankingLimit caps how many ranked errors a caller may request.
	maxRankingLimit = 100

	// maxRankingScan is how many rows are fetched before env filtering, so an
	// env-scoped caller still fills a page.
	maxRankingScan = 200
)

// fingerprintInEnv reports whether this fingerprint has an error group in env
// that the caller is allowed to read. It is the entitlement proof for the
// impact stores' fingerprint-only queries, which carry no environment of their
// own. No group store to check against means "cannot prove it" — deny.
func fingerprintInEnv(ctx context.Context, deps ErrorsDeps, fingerprint, env string) bool {
	if deps.ErrorGroupStore == nil {
		return false
	}
	eg, err := deps.ErrorGroupStore.Get(ctx, fingerprint, env)
	if err != nil {
		return false
	}
	return scopeAllowsEnv(ctx, eg.Environment)
}

// affectedUsersAllowed reports whether per-user data for a fingerprint may be
// shown. GetAffectedUsers/TopAffectedUsers are fingerprint-only lookups: for a
// fingerprint seen in several environments the user list mixes them, and an
// env-pinned token must not receive another env's user IDs.
func affectedUsersAllowed(ctx context.Context, deps ErrorsDeps, fingerprint, env string) bool {
	if !scopeIsPinned(ctx) {
		return true
	}
	if deps.ErrorGroupStore == nil {
		return false
	}
	eg, err := deps.ErrorGroupStore.Get(ctx, fingerprint, env)
	if err != nil || !scopeAllowsEnv(ctx, eg.Environment) {
		return false
	}
	// Seen in exactly one env (or unknown-but-single) — the user list cannot
	// contain anyone else's environment.
	return len(eg.SeenInEnvs) <= 1
}

// ---------------------------------------------------------------------------
// Action: impact — error impact analysis (from errorImpactHandler)
// ---------------------------------------------------------------------------

func ErrorsImpact(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorImpactStore == nil {
		return NewToolResultError("ErrorImpactStore not configured"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	// Resolve the caller's env up front: it is both the gate and, when the
	// impact row does not name its own env, the fallback proof of entitlement.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	// Env-scope gate. A pinned token reads impact for its OWN env only, so the
	// store is asked for that env's numbers rather than the cross-env
	// aggregate: the aggregate names only its highest-impact env, and gating on
	// that would deny a caller whose env simply scores lower. Entitlement is
	// still proven against the error group, since an env argument alone must
	// not conjure access to a fingerprint the caller cannot see.
	pinned := scopeIsPinned(ctx)
	if pinned && !fingerprintInEnv(ctx, deps, fingerprint, env) {
		return NewToolResultError("error group not found"), nil
	}

	// Unpinned callers keep the all-env aggregate ("").
	impactEnv := ""
	if pinned {
		impactEnv = env
	}

	impact, err := deps.ErrorImpactStore.GetImpact(ctx, fingerprint, impactEnv)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get impact: %v", err)), nil
	}

	// A cross-env aggregate must never be read as one env's number.
	crossEnvAggregate := impactEnv == "" && impact.Environment == ""
	if impactEnv == "" && impact.Environment != "" && !scopeAllowsEnv(ctx, impact.Environment) {
		return NewToolResultError("error group not found"), nil
	}

	resp := map[string]any{
		"fingerprint":       fingerprint,
		"unique_users":      impact.UniqueUsers,
		"total_occurrences": impact.TotalOccurrences,
		"impact_score":      impact.ImpactScore,
	}
	if impact.Environment != "" {
		resp["environment"] = impact.Environment
	}
	if crossEnvAggregate {
		// Never let an env-scoped caller read an all-env aggregate as if it were
		// their env's number.
		resp["scope_note"] = "The stored impact row does not name an environment; these totals aggregate every environment this fingerprint was seen in."
	}

	if impact.CommonTraits != nil {
		resp["common_traits"] = impact.CommonTraits
	}

	// Fetch affected users. GetAffectedUsers is fingerprint-only, so for a
	// fingerprint seen in several envs the list mixes them; withhold it rather
	// than hand production user IDs to a staging-scoped token.
	limit := ArgInt(args, "limit", 10, 100)

	var users []store.AffectedUser
	if !affectedUsersAllowed(ctx, deps, fingerprint, env) {
		resp["affected_users_withheld"] = "This fingerprint occurs in more than one environment and the store cannot attribute users per environment."
	} else {
		users, err = deps.ErrorImpactStore.GetAffectedUsers(ctx, fingerprint, limit)
	}
	if err == nil && len(users) > 0 {
		type userEntry struct {
			UserID          string `json:"user_id"`
			OccurrenceCount int    `json:"occurrence_count"`
			FirstSeenAt     string `json:"first_seen_at"`
			LastSeenAt      string `json:"last_seen_at"`
		}
		entries := make([]userEntry, len(users))
		for i, u := range users {
			entries[i] = userEntry{
				UserID:          u.UserID,
				OccurrenceCount: u.OccurrenceCount,
				FirstSeenAt:     u.FirstSeenAt.Format(time.RFC3339),
				LastSeenAt:      u.LastSeenAt.Format(time.RFC3339),
			}
		}
		resp["affected_users"] = entries
	}

	// Include error group info if available. Read the group from the same env
	// the impact row belongs to, so the message and status describe the impact
	// just reported rather than another env's copy of the fingerprint.
	if deps.ErrorGroupStore != nil {
		groupEnv := impact.Environment
		if groupEnv == "" {
			groupEnv = env
		}
		eg, egErr := deps.ErrorGroupStore.Get(ctx, fingerprint, groupEnv)
		if egErr == nil && scopeAllowsEnv(ctx, eg.Environment) {
			resp["exception_class"] = eg.ExceptionClass
			resp["message"] = eg.Message
			resp["service"] = eg.Service
			resp["status"] = string(eg.Status)
		}
	}

	// Suggestions.
	var suggestions []ToolSuggestion
	suggestions = append(suggestions, Suggest("errors", "View full error details and lifecycle", map[string]any{
		"action":      "detail",
		"fingerprint": fingerprint,
	}))
	if impact.UniqueUsers > 0 {
		suggestions = append(suggestions, Suggest("errors", "See all errors ranked by user impact", map[string]any{"action": "ranking"}))
	}
	return JSONResult(resp, suggestions...)
}

// ---------------------------------------------------------------------------
// Action: user_errors — errors for a user (from userErrorsHandler)
// ---------------------------------------------------------------------------

func ErrorsUserErrors(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorImpactStore == nil {
		return NewToolResultError("ErrorImpactStore not configured"), nil
	}

	userID := ArgString(args, "user_id")
	if userID == "" {
		return NewToolResultError("user_id is required"), nil
	}

	sinceStr := ArgStringDefault(args, "since", "24h")
	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}
	if duration <= 0 {
		return NewToolResultError(fmt.Sprintf("invalid since %q: must be a positive duration", sinceStr)), nil
	}
	since := time.Now().UTC().Add(-duration)

	// Env scope. GetUserErrors filters on user_id and last_seen_at only — it
	// joins error_groups across every environment — so without this an env-scoped
	// token could enumerate a production user's full error history.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	errors, err := deps.ErrorImpactStore.GetUserErrors(ctx, userID, since)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get user errors: %v", err)), nil
	}

	// ErrorSummary carries no environment, so each row is kept only if its
	// fingerprint has a group in the caller's env. Fail closed: with no group
	// store to check against, a pinned token gets nothing rather than everything.
	scopeFiltered := 0
	if scopeIsPinned(ctx) {
		kept := make([]store.ErrorSummary, 0, len(errors))
		for _, e := range errors {
			if fingerprintInEnv(ctx, deps, e.Fingerprint, env) {
				kept = append(kept, e)
			}
		}
		scopeFiltered = len(errors) - len(kept)
		errors = kept
	}

	if len(errors) == 0 {
		return EmptyResult(fmt.Sprintf("No errors found for user %s in the last %s.", userID, sinceStr))
	}

	type errorEntry struct {
		Fingerprint     string `json:"fingerprint"`
		ExceptionClass  string `json:"exception_class,omitempty"`
		Message         string `json:"message"`
		OccurrenceCount int    `json:"occurrence_count"`
		Status          string `json:"status"`
		FirstSeenAt     string `json:"first_seen_at"`
		LastSeenAt      string `json:"last_seen_at"`
	}

	entries := make([]errorEntry, len(errors))
	for i, e := range errors {
		msg := e.Message
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		entries[i] = errorEntry{
			Fingerprint:     e.Fingerprint,
			ExceptionClass:  e.ExceptionClass,
			Message:         msg,
			OccurrenceCount: e.OccurrenceCount,
			Status:          string(e.Status),
			FirstSeenAt:     e.FirstSeenAt.Format(time.RFC3339),
			LastSeenAt:      e.LastSeenAt.Format(time.RFC3339),
		}
	}

	resp := map[string]any{
		"user_id":     userID,
		"since":       since.Format(time.RFC3339),
		"error_count": len(entries),
		"errors":      entries,
	}
	if env != "" {
		resp["environment"] = env
	}
	if scopeFiltered > 0 {
		resp["out_of_scope_errors_hidden"] = scopeFiltered
	}

	// Suggest investigating the most recent error. Note: do NOT suggest
	// user_errors here — that is the call being answered, and re-issuing it
	// returns this same response.
	var suggestions []ToolSuggestion
	suggestions = append(suggestions, Suggest("errors", "See impact details for the top error", map[string]any{
		"action":      "impact",
		"fingerprint": entries[0].Fingerprint,
	}))
	suggestions = append(suggestions, Suggest("logs", "See the log lines where this error hit the user", map[string]any{
		"action":            "search",
		"error_fingerprint": entries[0].Fingerprint,
		"since":             sinceStr,
	}))
	return JSONResult(resp, suggestions...)
}

// ---------------------------------------------------------------------------
// Action: ranking — top errors by impact (from topErrorsByImpactHandler)
// ---------------------------------------------------------------------------

func ErrorsRanking(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorImpactStore == nil {
		return NewToolResultError("ErrorImpactStore not configured"), nil
	}

	// Env scope. Every sibling read action resolves or gates env; ranking used
	// to do neither, so a staging-scoped token was handed production
	// fingerprints, exception messages and affected user IDs.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	limit := ArgInt(args, "limit", 20, maxRankingLimit)

	params := store.ImpactQueryParams{
		Limit:       limit,
		Environment: env,
	}

	if v := ArgString(args, "status"); v != "" {
		params.Status = store.ErrorGroupStatus(v)
	}
	params.Service = ArgString(args, "service")
	if v := ArgString(args, "sort_by"); v != "" {
		params.SortBy = v
	}
	// Over-fetch when results have to be env-filtered here, so scoping does not
	// silently shrink the page.
	if scopeIsPinned(ctx) {
		params.Limit = maxRankingScan
	}

	sinceStr := ArgStringDefault(args, "since", "24h")
	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}
	if duration <= 0 {
		return NewToolResultError(fmt.Sprintf("invalid since %q: must be a positive duration", sinceStr)), nil
	}
	params.Since = time.Now().UTC().Add(-duration)

	results, err := deps.ErrorImpactStore.TopByImpact(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get top errors: %v", err)), nil
	}

	// Enforce the env scope on the results too: params.Environment is a request,
	// this is the guarantee. Rows whose env the token cannot read are dropped,
	// including rows with no env at all (see scopeAllowsEnv's empty-env rule).
	if scopeIsPinned(ctx) {
		kept := make([]store.ErrorGroupWithImpact, 0, len(results))
		for _, r := range results {
			if !scopeAllowsEnv(ctx, r.Environment) {
				continue
			}
			if env != "" && r.Environment != env {
				continue
			}
			kept = append(kept, r)
		}
		results = kept
	}
	if len(results) > limit {
		results = results[:limit]
	}

	if len(results) == 0 {
		return EmptyResult("No errors found with impact data.")
	}

	type impactEntry struct {
		Fingerprint     string   `json:"fingerprint"`
		Service         string   `json:"service"`
		Environment     string   `json:"environment,omitempty"`
		ExceptionClass  string   `json:"exception_class,omitempty"`
		Message         string   `json:"message"`
		Status          string   `json:"status"`
		OccurrenceCount int      `json:"occurrence_count"`
		UniqueUsers     int      `json:"unique_users"`
		ImpactScore     float64  `json:"impact_score"`
		Critical        bool     `json:"critical,omitempty"`
		LastSeenAt      string   `json:"last_seen_at"`
		TopUsers        []string `json:"top_users,omitempty"`
	}

	entries := make([]impactEntry, len(results))
	for i, r := range results {
		msg := r.Message
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		e := impactEntry{
			Fingerprint:     r.Fingerprint,
			Service:         r.Service,
			Environment:     r.Environment,
			ExceptionClass:  r.ExceptionClass,
			Message:         msg,
			Status:          string(r.Status),
			OccurrenceCount: r.OccurrenceCount,
			UniqueUsers:     r.UniqueUsers,
			ImpactScore:     r.ImpactScore,
			Critical:        isCriticalPath(deps.CriticalPaths, r.Service, r.Message, r.ExceptionClass),
			LastSeenAt:      r.LastSeenAt.Format(time.RFC3339),
		}
		// TopAffectedUsers comes from a fingerprint-only lookup, so it mixes
		// environments whenever the fingerprint exists in more than one.
		if affectedUsersAllowed(ctx, deps, r.Fingerprint, r.Environment) {
			for _, u := range r.TopAffectedUsers {
				e.TopUsers = append(e.TopUsers, u.UserID)
			}
		}
		entries[i] = e
	}

	// Money path to the top. The store ranks by user count, which is exactly
	// wrong when ten people cannot pay and a thousand cannot load an avatar.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Critical && !entries[j].Critical
	})

	resp := map[string]any{
		"since":        params.Since.Format(time.RFC3339),
		"result_count": len(entries),
		"errors":       entries,
	}
	if env != "" {
		resp["environment"] = env
	}

	// Suggest investigating the top error.
	var suggestions []ToolSuggestion
	suggestions = append(suggestions, Suggest("errors", "See detailed impact for the top error", map[string]any{
		"action":      "impact",
		"fingerprint": entries[0].Fingerprint,
	}))
	return JSONResult(resp, suggestions...)
}
