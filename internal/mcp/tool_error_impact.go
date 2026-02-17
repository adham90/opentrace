package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// errorImpactHandler returns impact analysis for an error group: affected users,
// common traits, and impact score.
func errorImpactHandler(eis store.ErrorImpactStore, egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if eis == nil {
			return mcp.NewToolResultError("ErrorImpactStore not configured"), nil
		}

		args := request.GetArguments()
		fingerprint, _ := args["fingerprint"].(string)
		if fingerprint == "" {
			return mcp.NewToolResultError("fingerprint is required"), nil
		}

		impact, err := eis.GetImpact(ctx, fingerprint)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get impact: %v", err)), nil
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
		limit := 10
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		users, err := eis.GetAffectedUsers(ctx, fingerprint, limit)
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
		if egs != nil {
			eg, err := egs.Get(ctx, fingerprint)
			if err == nil {
				resp["exception_class"] = eg.ExceptionClass
				resp["message"] = eg.Message
				resp["service"] = eg.Service
				resp["status"] = string(eg.Status)
			}
		}

		// Suggestions.
		var suggestions []ToolSuggestion
		suggestions = append(suggestions, suggest("error_detail", "View full error details and lifecycle", map[string]any{
			"fingerprint": fingerprint,
		}))
		if impact.UniqueUsers > 0 {
			suggestions = append(suggestions, suggest("top_errors_by_impact", "See all errors ranked by user impact", nil))
		}
		withSuggestions(resp, suggestions...)

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// userErrorsHandler returns errors affecting a specific user.
func userErrorsHandler(eis store.ErrorImpactStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if eis == nil {
			return mcp.NewToolResultError("ErrorImpactStore not configured"), nil
		}

		args := request.GetArguments()
		userID, _ := args["user_id"].(string)
		if userID == "" {
			return mcp.NewToolResultError("user_id is required"), nil
		}

		sinceStr := "24h"
		if v, ok := args["since"].(string); ok && v != "" {
			sinceStr = v
		}
		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}
		since := time.Now().UTC().Add(-duration)

		errors, err := eis.GetUserErrors(ctx, userID, since)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get user errors: %v", err)), nil
		}

		if len(errors) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No errors found for user %s in the last %s.", userID, sinceStr)), nil
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
		suggestions = append(suggestions, suggest("error_impact", "See impact details for the top error", map[string]any{
			"fingerprint": entries[0].Fingerprint,
		}))
		suggestions = append(suggestions, suggest("user_journey", "See what this user was doing", map[string]any{
			"user_id": userID,
		}))
		withSuggestions(resp, suggestions...)

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// topErrorsByImpactHandler returns errors ranked by user impact.
func topErrorsByImpactHandler(eis store.ErrorImpactStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if eis == nil {
			return mcp.NewToolResultError("ErrorImpactStore not configured"), nil
		}

		args := request.GetArguments()

		params := store.ImpactQueryParams{
			Limit: 20,
		}

		if v, ok := args["status"].(string); ok && v != "" {
			params.Status = store.ErrorGroupStatus(v)
		}
		if v, ok := args["service"].(string); ok && v != "" {
			params.Service = v
		}
		if v, ok := args["sort_by"].(string); ok && v != "" {
			params.SortBy = v
		}
		if v, ok := args["limit"].(float64); ok && v > 0 {
			params.Limit = int(v)
		}

		sinceStr := "24h"
		if v, ok := args["since"].(string); ok && v != "" {
			sinceStr = v
		}
		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}
		params.Since = time.Now().UTC().Add(-duration)

		results, err := eis.TopByImpact(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get top errors: %v", err)), nil
		}

		if len(results) == 0 {
			return mcp.NewToolResultText("No errors found with impact data."), nil
		}

		type impactEntry struct {
			Fingerprint     string `json:"fingerprint"`
			Service         string `json:"service"`
			ExceptionClass  string `json:"exception_class,omitempty"`
			Message         string `json:"message"`
			Status          string `json:"status"`
			OccurrenceCount int    `json:"occurrence_count"`
			UniqueUsers     int    `json:"unique_users"`
			ImpactScore     float64 `json:"impact_score"`
			LastSeenAt      string `json:"last_seen_at"`
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
		suggestions = append(suggestions, suggest("error_impact", "See detailed impact for the top error", map[string]any{
			"fingerprint": entries[0].Fingerprint,
		}))
		withSuggestions(resp, suggestions...)

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}
