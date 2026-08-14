package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Action: detail — get error group details (from errorDetailHandler)
// ---------------------------------------------------------------------------

const (
	// recentOccurrencesWindow is how far back from the group's last_seen_at the
	// occurrence lookup reaches.
	recentOccurrencesWindow = 24 * time.Hour

	// recentOccurrencesLimit is how many occurrence log lines are returned.
	recentOccurrencesLimit = 5

	// lifecycleEventLimit is how many lifecycle events are returned. Events are
	// read for one environment, so the budget is not shared across envs.
	lifecycleEventLimit = 10
)

func ErrorsDetail(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	// Resolve the env first and fetch that env's row. Fetching by fingerprint
	// alone returns whichever env was seen most recently, so a production-scoped
	// caller asking about a fingerprint that exists in both envs used to get
	// staging's row — and then be denied by the gate below, reporting "not
	// found" for a group it is fully entitled to read.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	eg, err := deps.ErrorGroupStore.Get(ctx, fingerprint, env)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("error group not found: %v", err)), nil
	}

	// Still gate the result: with an unscoped or wildcard token env resolves to
	// "", so the row can come from any env. Same "not found" message either way,
	// to avoid revealing cross-env existence.
	if !scopeAllowsEnv(ctx, eg.Environment) {
		return NewToolResultError("error group not found"), nil
	}

	// Fetch lifecycle events for THIS group's environment. Events are written
	// per (fingerprint, environment); reading them by fingerprint alone showed
	// staging's resolve reasons as this production group's history.
	events, err := deps.ErrorGroupStore.ListEvents(ctx, fingerprint, eg.Environment, lifecycleEventLimit)
	if err != nil {
		slog.Warn("error detail lifecycle events unavailable",
			"event", "errors_detail_events_failed",
			"fingerprint", fingerprint,
			"environment", eg.Environment,
			"error", err,
		)
	}

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

	// Fetch recent occurrences from logs. The window is derived from the group's
	// own last_seen_at, not from now: with Start left nil the store applies a
	// now-1h default, so any group that last fired more than an hour ago
	// reported zero occurrences right next to a last_seen_at of yesterday.
	if deps.LogStore != nil {
		occStart := eg.LastSeenAt.Add(-recentOccurrencesWindow)
		if eg.FirstSeenAt.After(occStart) {
			occStart = eg.FirstSeenAt
		}
		occEnd := eg.LastSeenAt.Add(time.Minute)
		recentLogs, _ := deps.LogStore.Search(ctx, store.LogSearchParams{
			ErrorFingerprint: fingerprint,
			Environment:      eg.Environment,
			Start:            &occStart,
			End:              &occEnd,
			Limit:            recentOccurrencesLimit,
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
