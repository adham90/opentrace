package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

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

	impact, err := deps.ErrorImpactStore.GetImpact(ctx, fingerprint)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get impact: %v", err)), nil
	}

	resp := map[string]any{
		"fingerprint":       fingerprint,
		"unique_users":      impact.UniqueUsers,
		"total_occurrences": impact.TotalOccurrences,
		"impact_score":      impact.ImpactScore,
	}

	if impact.CommonTraits != nil {
		resp["common_traits"] = impact.CommonTraits
	}

	// Fetch affected users.
	limit := ArgInt(args, "limit", 10, 100)

	users, err := deps.ErrorImpactStore.GetAffectedUsers(ctx, fingerprint, limit)
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

	// Include error group info if available.
	if deps.ErrorGroupStore != nil {
		eg, egErr := deps.ErrorGroupStore.Get(ctx, fingerprint)
		if egErr == nil {
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
	since := time.Now().UTC().Add(-duration)

	errors, err := deps.ErrorImpactStore.GetUserErrors(ctx, userID, since)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get user errors: %v", err)), nil
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

	// Suggest investigating the most recent error.
	var suggestions []ToolSuggestion
	suggestions = append(suggestions, Suggest("errors", "See impact details for the top error", map[string]any{
		"action":      "impact",
		"fingerprint": entries[0].Fingerprint,
	}))
	suggestions = append(suggestions, Suggest("errors", "See what this user was doing", map[string]any{
		"action":  "user_errors",
		"user_id": userID,
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

	params := store.ImpactQueryParams{
		Limit: 20,
	}

	if v := ArgString(args, "status"); v != "" {
		params.Status = store.ErrorGroupStatus(v)
	}
	params.Service = ArgString(args, "service")
	if v := ArgString(args, "sort_by"); v != "" {
		params.SortBy = v
	}
	params.Limit = ArgInt(args, "limit", 20, 100)

	sinceStr := ArgStringDefault(args, "since", "24h")
	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}
	params.Since = time.Now().UTC().Add(-duration)

	results, err := deps.ErrorImpactStore.TopByImpact(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get top errors: %v", err)), nil
	}

	if len(results) == 0 {
		return EmptyResult("No errors found with impact data.")
	}

	type impactEntry struct {
		Fingerprint     string   `json:"fingerprint"`
		Service         string   `json:"service"`
		ExceptionClass  string   `json:"exception_class,omitempty"`
		Message         string   `json:"message"`
		Status          string   `json:"status"`
		OccurrenceCount int      `json:"occurrence_count"`
		UniqueUsers     int      `json:"unique_users"`
		ImpactScore     float64  `json:"impact_score"`
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
			ExceptionClass:  r.ExceptionClass,
			Message:         msg,
			Status:          string(r.Status),
			OccurrenceCount: r.OccurrenceCount,
			UniqueUsers:     r.UniqueUsers,
			ImpactScore:     r.ImpactScore,
			LastSeenAt:      r.LastSeenAt.Format(time.RFC3339),
		}
		for _, u := range r.TopAffectedUsers {
			e.TopUsers = append(e.TopUsers, u.UserID)
		}
		entries[i] = e
	}

	resp := map[string]any{
		"since":        params.Since.Format(time.RFC3339),
		"result_count": len(entries),
		"errors":       entries,
	}

	// Suggest investigating the top error.
	var suggestions []ToolSuggestion
	suggestions = append(suggestions, Suggest("errors", "See detailed impact for the top error", map[string]any{
		"action":      "impact",
		"fingerprint": entries[0].Fingerprint,
	}))
	return JSONResult(resp, suggestions...)
}
