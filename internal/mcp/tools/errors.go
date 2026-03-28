package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"


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
		action, _ := args["action"].(string)

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

// ---------------------------------------------------------------------------
// Action: list — list error groups (from errorGroupsHandler)
// ---------------------------------------------------------------------------

func ErrorsList(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

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

	groups, err := deps.ErrorGroupStore.List(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list error groups: %v", err)), nil
	}

	if len(groups) == 0 {
		return NewToolResultText("No error groups found matching the criteria."), nil
	}

	// Count totals for context.
	unresolvedCount, _ := deps.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved)

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
		suggestions = append(suggestions, Suggest("errors", "Investigate the most frequent error", map[string]any{"action": "detail", "fingerprint": summaries[0].Fingerprint}))
	}
	if unresolvedCount > 5 {
		suggestions = append(suggestions, Suggest("overview", "Get full system health overview", map[string]any{"action": "diagnose"}))
	}
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: detail — get error group details (from errorDetailHandler)
// ---------------------------------------------------------------------------

func ErrorsDetail(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint, _ := args["fingerprint"].(string)
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	eg, err := deps.ErrorGroupStore.Get(ctx, fingerprint)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("error group not found: %v", err)), nil
	}

	// Link investigated error to investigation session.
	if deps.Recurrence != nil && deps.Session != nil {
		if sid := deps.Session.CurrentSessionID(); sid != "" {
			deps.Recurrence.LinkInvestigatedError(ctx, sid, fingerprint)
			if eg.ReopenedCount > 0 {
				deps.Recurrence.DetectErrorRecurrence(ctx, sid, fingerprint, eg.ReopenedCount)
			}
		}
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

	// Inject recurrence context for reopened errors.
	if eg.ReopenedCount > 0 && deps.Recurrence != nil && deps.Session != nil {
		if sid := deps.Session.CurrentSessionID(); sid != "" {
			deps.Recurrence.InjectRecurrenceContext(ctx, sid, resp)
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
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: investigate — deep investigate (from investigateErrorHandler)
// ---------------------------------------------------------------------------

func ErrorsInvestigate(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.LogStore == nil {
		return NewToolResultError("LogStore not configured"), nil
	}

	var anchor *store.LogEntry
	var err error

	// Accept either log_id or trace_id as the entry point.
	if logID, ok := args["log_id"].(float64); ok && logID > 0 {
		anchor, err = deps.LogStore.GetByID(ctx, int64(logID))
		if err != nil {
			return NewToolResultError(fmt.Sprintf("log entry %d not found: %v", int64(logID), err)), nil
		}
	} else if traceID, ok := args["trace_id"].(string); ok && traceID != "" {
		// Find the primary error entry in this trace.
		traceEntries, searchErr := deps.LogStore.Search(ctx, store.LogSearchParams{
			TraceID: traceID,
			Limit:   50,
			SortAsc: true,
		})
		if searchErr != nil || len(traceEntries) == 0 {
			return NewToolResultError(fmt.Sprintf("no log entries found for trace_id=%s", traceID)), nil
		}
		// Pick the first error-level entry, or the last entry.
		picked := &traceEntries[len(traceEntries)-1]
		for i := range traceEntries {
			if traceEntries[i].Level == "ERROR" || traceEntries[i].Level == "FATAL" {
				picked = &traceEntries[i]
				break
			}
		}
		anchor = picked
	} else {
		return NewToolResultError("Either log_id (positive integer) or trace_id (string) is required for the investigate action"), nil
	}

	// Link investigated error to investigation session.
	if anchor.ErrorFingerprint != "" && deps.Recurrence != nil && deps.Session != nil {
		if sid := deps.Session.CurrentSessionID(); sid != "" {
			deps.Recurrence.LinkInvestigatedError(ctx, sid, anchor.ErrorFingerprint)
		}
	}

	resp := make(map[string]any)

	// 1. Core log entry.
	resp["log_entry"] = map[string]any{
		"id":          anchor.ID,
		"timestamp":   anchor.Timestamp.Format(time.RFC3339Nano),
		"level":       anchor.Level,
		"service":     anchor.Service,
		"environment": anchor.Environment,
		"message":     anchor.Message,
		"trace_id":    anchor.TraceID,
		"request_id":  anchor.RequestID,
	}

	// 2. Exception details — pull from both top-level fields and metadata.
	excSection := make(map[string]any)
	hasException := false

	if anchor.ExceptionClass != "" {
		excSection["exception_class"] = anchor.ExceptionClass
		hasException = true
	}
	if anchor.ErrorFingerprint != "" {
		excSection["error_fingerprint"] = anchor.ErrorFingerprint
	}
	if anchor.SourceFile != "" {
		excSection["source_file"] = anchor.SourceFile
		if anchor.SourceLine > 0 {
			excSection["source_line"] = anchor.SourceLine
		}
	}
	// Extract from metadata.
	if msg, ok := anchor.Metadata["exception_message"]; ok {
		excSection["exception_message"] = msg
		hasException = true
	}
	if bt, ok := anchor.Metadata["backtrace"]; ok {
		excSection["backtrace"] = bt
		hasException = true
	}
	if causes, ok := anchor.Metadata["exception_causes"]; ok {
		excSection["exception_causes"] = causes
	}
	if handled, ok := anchor.Metadata["handled"]; ok {
		excSection["handled"] = handled
	}
	if src, ok := anchor.Metadata["source_context"]; ok {
		excSection["source_context"] = src
	}

	if hasException {
		resp["exception"] = excSection
	}

	// 3. Request params (from metadata).
	if params, ok := anchor.Metadata["params"]; ok {
		resp["request_params"] = params
	}

	// 4. Request summary (if present).
	if anchor.RequestSummary != nil {
		rs := anchor.RequestSummary
		resp["request_summary"] = map[string]any{
			"controller":    rs.Controller,
			"action":        rs.Action,
			"method":        rs.Method,
			"path":          rs.Path,
			"status":        rs.Status,
			"duration_ms":   rs.DurationMs,
			"sql_count":     rs.SQLCount,
			"sql_total_ms":  rs.SQLTotalMs,
			"n_plus_one":    rs.NPlusOne,
			"view_count":    rs.ViewCount,
			"view_total_ms": rs.ViewTotalMs,
		}
	}

	// 5. Related trace entries (SQL queries, views, other logs in same request).
	if anchor.TraceID != "" {
		traceEntries, _ := deps.LogStore.Search(ctx, store.LogSearchParams{
			TraceID: anchor.TraceID,
			Limit:   30,
			SortAsc: true,
		})
		if len(traceEntries) > 1 {
			var traceTimeline []map[string]any
			for _, e := range traceEntries {
				if e.ID == anchor.ID {
					continue // skip the anchor itself
				}
				entry := map[string]any{
					"id":        e.ID,
					"timestamp": e.Timestamp.Format(time.RFC3339Nano),
					"level":     e.Level,
					"message":   truncate(e.Message, 200),
				}
				if e.EventType != "" {
					entry["event_type"] = e.EventType
				}
				if e.ExceptionClass != "" {
					entry["exception_class"] = e.ExceptionClass
				}
				traceTimeline = append(traceTimeline, entry)
			}
			if len(traceTimeline) > 0 {
				resp["trace_timeline"] = traceTimeline
			}
		}
	}

	// 6. Surrounding logs (context window).
	beforeEntries, _ := deps.LogStore.Search(ctx, store.LogSearchParams{
		End:     &anchor.Timestamp,
		Service: anchor.Service,
		Limit:   5,
		SortAsc: false,
	})
	afterStart := anchor.Timestamp.Add(time.Millisecond) // avoid self
	afterEntries, _ := deps.LogStore.Search(ctx, store.LogSearchParams{
		Start:   &afterStart,
		Service: anchor.Service,
		Limit:   5,
		SortAsc: true,
	})

	var contextEntries []map[string]any
	// Reverse before entries to show chronological order.
	for i := len(beforeEntries) - 1; i >= 0; i-- {
		e := beforeEntries[i]
		if e.ID == anchor.ID {
			continue
		}
		entry := map[string]any{
			"id":        e.ID,
			"timestamp": e.Timestamp.Format(time.RFC3339Nano),
			"level":     e.Level,
			"message":   truncate(e.Message, 200),
			"position":  "before",
		}
		if e.ExceptionClass != "" {
			entry["exception_class"] = e.ExceptionClass
		}
		contextEntries = append(contextEntries, entry)
	}
	for _, e := range afterEntries {
		if e.ID == anchor.ID {
			continue
		}
		entry := map[string]any{
			"id":        e.ID,
			"timestamp": e.Timestamp.Format(time.RFC3339Nano),
			"level":     e.Level,
			"message":   truncate(e.Message, 200),
			"position":  "after",
		}
		if e.ExceptionClass != "" {
			entry["exception_class"] = e.ExceptionClass
		}
		contextEntries = append(contextEntries, entry)
	}
	if len(contextEntries) > 0 {
		resp["surrounding_logs"] = contextEntries
	}

	// 7. Error group (if fingerprint exists).
	if anchor.ErrorFingerprint != "" && deps.ErrorGroupStore != nil {
		if eg, egErr := deps.ErrorGroupStore.Get(ctx, anchor.ErrorFingerprint); egErr == nil {
			resp["error_group"] = map[string]any{
				"fingerprint":      eg.Fingerprint,
				"status":           string(eg.Status),
				"occurrence_count": eg.OccurrenceCount,
				"first_seen_at":    eg.FirstSeenAt.Format(time.RFC3339),
				"last_seen_at":     eg.LastSeenAt.Format(time.RFC3339),
				"reopened_count":   eg.ReopenedCount,
			}

			// Detect and inject recurrence context for reopened errors.
			if eg.ReopenedCount > 0 && deps.Recurrence != nil && deps.Session != nil {
				if sid := deps.Session.CurrentSessionID(); sid != "" {
					deps.Recurrence.DetectErrorRecurrence(ctx, sid, anchor.ErrorFingerprint, eg.ReopenedCount)
					deps.Recurrence.InjectRecurrenceContext(ctx, sid, resp)
				}
			}
		}
	}

	// Suggested next steps.
	var suggestions []ToolSuggestion
	if anchor.ErrorFingerprint != "" {
		suggestions = append(suggestions, Suggest("errors", "Full error group history and lifecycle", map[string]any{
			"action":      "detail",
			"fingerprint": anchor.ErrorFingerprint,
		}))
		suggestions = append(suggestions, Suggest("logs", "Find all occurrences of this error", map[string]any{
			"action":            "search",
			"error_fingerprint": anchor.ErrorFingerprint,
		}))
	}
	if anchor.ExceptionClass != "" {
		suggestions = append(suggestions, Suggest("logs", "Find all logs with this exception class", map[string]any{
			"action":          "search",
			"exception_class": anchor.ExceptionClass,
		}))
	}
	if anchor.ErrorFingerprint != "" {
		suggestions = append(suggestions, Suggest("errors", "Mark as resolved after fixing", map[string]any{
			"action":      "resolve",
			"fingerprint": anchor.ErrorFingerprint,
		}))
	}
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: impact — error impact analysis (from errorImpactHandler)
// ---------------------------------------------------------------------------

func ErrorsImpact(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorImpactStore == nil {
		return NewToolResultError("ErrorImpactStore not configured"), nil
	}

	fingerprint, _ := args["fingerprint"].(string)
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
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

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
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: user_errors — errors for a user (from userErrorsHandler)
// ---------------------------------------------------------------------------

func ErrorsUserErrors(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorImpactStore == nil {
		return NewToolResultError("ErrorImpactStore not configured"), nil
	}

	userID, _ := args["user_id"].(string)
	if userID == "" {
		return NewToolResultError("user_id is required"), nil
	}

	sinceStr := "24h"
	if v, ok := args["since"].(string); ok && v != "" {
		sinceStr = v
	}
	duration, err := parseTimeRange(sinceStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}
	since := time.Now().UTC().Add(-duration)

	errors, err := deps.ErrorImpactStore.GetUserErrors(ctx, userID, since)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get user errors: %v", err)), nil
	}

	if len(errors) == 0 {
		return NewToolResultText(fmt.Sprintf("No errors found for user %s in the last %s.", userID, sinceStr)), nil
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
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
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
		return NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}
	params.Since = time.Now().UTC().Add(-duration)

	results, err := deps.ErrorImpactStore.TopByImpact(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get top errors: %v", err)), nil
	}

	if len(results) == 0 {
		return NewToolResultText("No errors found with impact data."), nil
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
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: resolve — resolve error group (from resolveErrorHandler)
// ---------------------------------------------------------------------------

func ErrorsResolve(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint, _ := args["fingerprint"].(string)
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	reason, _ := args["reason"].(string)
	if reason == "" {
		return NewToolResultError("reason is required (e.g. 'Fixed in PR #42')"), nil
	}

	if err := deps.ErrorGroupStore.Resolve(ctx, fingerprint, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to resolve: %v", err)), nil
	}

	// Link resolved error to investigation session.
	if deps.Recurrence != nil && deps.Session != nil {
		if sid := deps.Session.CurrentSessionID(); sid != "" {
			deps.Recurrence.LinkResolvedError(ctx, sid, fingerprint)
		}
	}

	resp := map[string]any{
		"status":      "resolved",
		"fingerprint": fingerprint,
		"reason":      reason,
		"message":     "Error group marked as resolved. It will auto-reopen if the error recurs.",
	}
	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: ignore — ignore error group (from ignoreErrorHandler)
// ---------------------------------------------------------------------------

func ErrorsIgnore(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	fingerprint, _ := args["fingerprint"].(string)
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	reason, _ := args["reason"].(string)
	if reason == "" {
		return NewToolResultError("reason is required (e.g. 'Known noise from health checks')"), nil
	}

	if err := deps.ErrorGroupStore.Ignore(ctx, fingerprint, reason); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to ignore: %v", err)), nil
	}

	// Link resolved (ignored) error to investigation session.
	if deps.Recurrence != nil && deps.Session != nil {
		if sid := deps.Session.CurrentSessionID(); sid != "" {
			deps.Recurrence.LinkResolvedError(ctx, sid, fingerprint)
		}
	}

	resp := map[string]any{
		"status":      "ignored",
		"fingerprint": fingerprint,
		"reason":      reason,
		"message":     "Error group permanently ignored. New occurrences will still be counted but won't reopen the group.",
	}
	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: new — errors first seen within the given time window
// ---------------------------------------------------------------------------

func HandleNewErrors(ctx context.Context, deps ErrorsDeps, args map[string]any) (*CallToolResult, error) {
	if deps.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	since := GetSinceParam(args, 24*time.Hour)
	service, _ := args["service"].(string)

	groups, err := deps.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
		Service: service,
		Limit:   20,
		SortBy:  "first_seen_at",
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list error groups: %v", err)), nil
	}

	// Filter to only errors first seen after the since cutoff.
	type newError struct {
		Fingerprint     string `json:"fingerprint"`
		ExceptionClass  string `json:"exception_class,omitempty"`
		Message         string `json:"message"`
		Service         string `json:"service"`
		OccurrenceCount int    `json:"occurrence_count"`
		FirstSeenAt     string `json:"first_seen_at"`
	}

	var newErrors []newError
	for _, g := range groups {
		if g.FirstSeenAt.After(since) {
			newErrors = append(newErrors, newError{
				Fingerprint:     g.Fingerprint,
				ExceptionClass:  g.ExceptionClass,
				Message:         truncate(g.Message, 100),
				Service:         g.Service,
				OccurrenceCount: g.OccurrenceCount,
				FirstSeenAt:     g.FirstSeenAt.Format(time.RFC3339),
			})
		}
	}

	if len(newErrors) == 0 {
		return NewToolResultText("No new errors found in the given time window."), nil
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
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Helpers (local to this package to avoid circular imports)
// ---------------------------------------------------------------------------

// truncate returns the first n characters of s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
