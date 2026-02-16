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

// errorGroupsHandler lists error groups sorted by occurrence count or last seen.
func errorGroupsHandler(egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if egs == nil {
			return mcp.NewToolResultError("ErrorGroupStore not configured"), nil
		}

		args := request.GetArguments()

		params := store.ListErrorGroupParams{
			Limit: 20,
		}
		if v, ok := args["status"].(string); ok && v != "" {
			params.Status = store.ErrorGroupStatus(v)
		}
		if v, ok := args["service"].(string); ok && v != "" {
			params.Service = v
		}
		if v, ok := args["environment"].(string); ok && v != "" {
			params.Environment = v
		}
		if v, ok := args["sort_by"].(string); ok && v != "" {
			params.SortBy = v
		}
		if v, ok := args["limit"].(float64); ok && v > 0 {
			params.Limit = int(v)
			if params.Limit > 100 {
				params.Limit = 100
			}
		}

		groups, err := egs.List(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list error groups: %v", err)), nil
		}

		if len(groups) == 0 {
			return mcp.NewToolResultText("No error groups found matching the criteria."), nil
		}

		// Count totals for context.
		unresolvedCount, _ := egs.Count(ctx, store.ErrorGroupUnresolved)

		type groupSummary struct {
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

		summaries := make([]groupSummary, len(groups))
		for i, g := range groups {
			summaries[i] = groupSummary{
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

		resp := map[string]any{
			"total_unresolved": unresolvedCount,
			"returned":         len(summaries),
			"error_groups":     summaries,
		}

		// Suggest investigating the top error.
		var suggestions []ToolSuggestion
		if len(summaries) > 0 {
			suggestions = append(suggestions, suggest("error_detail", "Investigate the most frequent error", map[string]any{"fingerprint": summaries[0].Fingerprint}))
		}
		if unresolvedCount > 5 {
			suggestions = append(suggestions, suggest("diagnose", "Get full system health overview", nil))
		}
		withSuggestions(resp, suggestions...)

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// errorDetailHandler returns full details for a single error group.
func errorDetailHandler(egs store.ErrorGroupStore, ls store.LogStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if egs == nil {
			return mcp.NewToolResultError("ErrorGroupStore not configured"), nil
		}

		args := request.GetArguments()
		fingerprint, _ := args["fingerprint"].(string)
		if fingerprint == "" {
			return mcp.NewToolResultError("fingerprint is required"), nil
		}

		eg, err := egs.Get(ctx, fingerprint)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error group not found: %v", err)), nil
		}

		// Fetch lifecycle events.
		events, _ := egs.ListEvents(ctx, fingerprint, 10)

		type eventSummary struct {
			Action    string `json:"action"`
			Reason    string `json:"reason,omitempty"`
			CreatedAt string `json:"created_at"`
		}

		evSummaries := make([]eventSummary, len(events))
		for i, ev := range events {
			evSummaries[i] = eventSummary{
				Action:    ev.Action,
				Reason:    ev.Reason,
				CreatedAt: ev.CreatedAt.Format(time.RFC3339),
			}
		}

		resp := map[string]any{
			"fingerprint":      eg.Fingerprint,
			"service":          eg.Service,
			"environment":      eg.Environment,
			"exception_class":  eg.ExceptionClass,
			"message":          eg.Message,
			"source_file":      eg.SourceFile,
			"source_line":      eg.SourceLine,
			"status":           string(eg.Status),
			"occurrence_count": eg.OccurrenceCount,
			"reopened_count":   eg.ReopenedCount,
			"first_seen_at":    eg.FirstSeenAt.Format(time.RFC3339),
			"last_seen_at":     eg.LastSeenAt.Format(time.RFC3339),
			"events":           evSummaries,
		}

		// Fetch recent occurrences from logs.
		if ls != nil {
			recentLogs, _ := ls.Search(ctx, store.LogSearchParams{
				ErrorFingerprint: fingerprint,
				Limit:            5,
			})
			if len(recentLogs) > 0 {
				type logEntry struct {
					ID        int64  `json:"id"`
					Timestamp string `json:"timestamp"`
					Level     string `json:"level"`
					Message   string `json:"message"`
					TraceID   string `json:"trace_id,omitempty"`
				}
				entries := make([]logEntry, len(recentLogs))
				for i, l := range recentLogs {
					msg := l.Message
					if len(msg) > 200 {
						msg = msg[:200] + "..."
					}
					entries[i] = logEntry{
						ID:        l.ID,
						Timestamp: l.Timestamp.Format(time.RFC3339),
						Level:     l.Level,
						Message:   msg,
						TraceID:   l.TraceID,
					}
				}
				resp["recent_occurrences"] = entries
			}
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// resolveErrorHandler marks an error group as resolved.
func resolveErrorHandler(egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if egs == nil {
			return mcp.NewToolResultError("ErrorGroupStore not configured"), nil
		}

		args := request.GetArguments()
		fingerprint, _ := args["fingerprint"].(string)
		if fingerprint == "" {
			return mcp.NewToolResultError("fingerprint is required"), nil
		}

		reason, _ := args["reason"].(string)
		if reason == "" {
			return mcp.NewToolResultError("reason is required (e.g. 'Fixed in PR #42')"), nil
		}

		if err := egs.Resolve(ctx, fingerprint, reason); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to resolve: %v", err)), nil
		}

		resp := map[string]any{
			"status":      "resolved",
			"fingerprint": fingerprint,
			"reason":      reason,
			"message":     "Error group marked as resolved. It will auto-reopen if the error recurs.",
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// ignoreErrorHandler permanently ignores an error group.
func ignoreErrorHandler(egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if egs == nil {
			return mcp.NewToolResultError("ErrorGroupStore not configured"), nil
		}

		args := request.GetArguments()
		fingerprint, _ := args["fingerprint"].(string)
		if fingerprint == "" {
			return mcp.NewToolResultError("fingerprint is required"), nil
		}

		reason, _ := args["reason"].(string)
		if reason == "" {
			return mcp.NewToolResultError("reason is required (e.g. 'Known noise from health checks')"), nil
		}

		if err := egs.Ignore(ctx, fingerprint, reason); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to ignore: %v", err)), nil
		}

		resp := map[string]any{
			"status":      "ignored",
			"fingerprint": fingerprint,
			"reason":      reason,
			"message":     "Error group permanently ignored. New occurrences will still be counted but won't reopen the group.",
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}
