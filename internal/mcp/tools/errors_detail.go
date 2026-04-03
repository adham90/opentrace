package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Action: detail — get error group details (from errorDetailHandler)
// ---------------------------------------------------------------------------

func ErrorsDetail(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	eg, err := deps.ErrorGroupStore.Get(ctx, fingerprint)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("error group not found: %v", err)), nil
	}

	// Fetch lifecycle events.
	events, _ := deps.ErrorGroupStore.ListEvents(ctx, fingerprint, 10)

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
	if deps.LogStore != nil {
		recentLogs, _ := deps.LogStore.Search(ctx, store.LogSearchParams{
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

	// Suggest next steps.
	var suggestions []ToolSuggestion
	if eg.ExceptionClass != "" {
		sArgs := map[string]any{"exception_class": eg.ExceptionClass}
		if eg.Service != "" {
			sArgs["service"] = eg.Service
		}
		sArgs["action"] = "search"
		suggestions = append(suggestions, Suggest("logs", "Find all occurrences of this exception", sArgs))
	}
	if eg.Status == store.ErrorGroupUnresolved {
		suggestions = append(suggestions, Suggest("errors", "Mark as resolved after fixing", map[string]any{
			"action":      "resolve",
			"fingerprint": eg.Fingerprint,
		}))
	}
	return JSONResult(resp, suggestions...)
}
