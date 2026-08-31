package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// The single most common support question is "customer X says it's broken" —
// not "what is the aggregate error rate". Everything here is a filter and a
// sort over columns already stored (user_id, tenant_id, session_id): no new
// storage, no new ingest path.

// maxUsersLimit caps any listing this tool returns.
const maxUsersLimit = 200

// defaultUsersLimit applies when the caller does not ask for one.
const defaultUsersLimit = 50

// defaultUsersWindow is the lookback when no window is given. A support
// conversation is about the recent past; an unbounded default would scan every
// segment on disk to answer "what happened to this person just now".
const defaultUsersWindow = 24 * time.Hour

// UsersDeps holds the stores the users tool needs.
type UsersDeps struct {
	LogStore         store.LogStore
	ErrorImpactStore store.ErrorImpactStore
	ErrorGroupStore  store.ErrorGroupStore
}

// UsersHandler returns a handler for the consolidated users tool.
func UsersHandler(d UsersDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)

		switch ArgString(args, "action") {
		case "timeline":
			return HandleUserTimeline(ctx, d, args)
		case "errors":
			return HandleUserErrors(ctx, d, args)
		case "impact":
			return HandleUserImpact(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf(
				"unknown action: %s (use timeline, errors, impact)", ArgString(args, "action"))), nil
		}
	}
}

// userTimelineEntry is one thing that happened to one person.
type userTimelineEntry struct {
	Time        string `json:"time"`
	Kind        string `json:"kind,omitempty"`
	Level       string `json:"level,omitempty"`
	Service     string `json:"service,omitempty"`
	Message     string `json:"message"`
	Method      string `json:"method,omitempty"`
	Path        string `json:"path,omitempty"`
	Status      int    `json:"status,omitempty"`
	DurationMs  int    `json:"duration_ms,omitempty"`
	ErrorClass  string `json:"error_class,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	LogID       int64  `json:"log_id,omitempty"`
}

// HandleUserTimeline returns everything one user or tenant hit, newest first.
func HandleUserTimeline(ctx context.Context, d UsersDeps, args map[string]any) (*CallToolResult, error) {
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	if d.LogStore == nil {
		return NewToolResultError("log store is unavailable"), nil
	}

	userID := ArgString(args, "user_id")
	tenantID := ArgString(args, "tenant_id")
	if userID == "" && tenantID == "" {
		return NewToolResultError("user_id or tenant_id is required"), nil
	}

	since, window, err := ResolveWindow(args, "24h")
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	entries, err := d.LogStore.Search(ctx, store.LogSearchParams{
		UserID:      userID,
		TenantID:    tenantID,
		Environment: env,
		Level:       ArgString(args, "level"),
		Service:     ArgString(args, "service"),
		Start:       &since,
		Limit:       usersLimit(args),
	})
	if err != nil {
		return NewToolResultError("searching this user's activity failed"), nil
	}

	items := make([]userTimelineEntry, 0, len(entries))
	errorCount := 0
	for _, e := range entries {
		if e.Level == "error" || e.Level == "fatal" {
			errorCount++
		}
		items = append(items, userTimelineEntry{
			Time:        e.Timestamp.Format(time.RFC3339),
			Kind:        e.Kind,
			Level:       e.Level,
			Service:     e.Service,
			Message:     e.Message,
			Status:      e.Status,
			DurationMs:  int(e.DurationMs),
			ErrorClass:  e.ExceptionClass,
			Fingerprint: e.ErrorFingerprint,
			TraceID:     e.TraceID,
			LogID:       e.ID,
		})
		if e.RequestSummary != nil {
			last := &items[len(items)-1]
			last.Method = e.RequestSummary.Method
			last.Path = e.RequestSummary.Path
			if last.Status == 0 {
				last.Status = e.RequestSummary.Status
			}
			if last.DurationMs == 0 {
				last.DurationMs = int(e.RequestSummary.DurationMs)
			}
		}
	}

	subject := userID
	if subject == "" {
		subject = tenantID
	}
	resp := map[string]any{
		"subject":     subject,
		"window":      window,
		"since":       since.Format(time.RFC3339),
		"count":       len(items),
		"error_count": errorCount,
		"items":       items,
	}
	if len(items) == 0 {
		resp["summary"] = fmt.Sprintf("No activity for %q in the last %s.", subject, window)
		return JSONResult(resp)
	}

	var suggestions []ToolSuggestion
	if errorCount > 0 {
		suggestions = append(suggestions, Suggest("users", "Which errors this user hit", map[string]any{
			"action": "errors", "user_id": userID,
		}))
	}
	for _, it := range items {
		if it.TraceID != "" {
			suggestions = append(suggestions, Suggest("logs", "Assemble the trace around this request", map[string]any{
				"action": "trace", "trace_id": it.TraceID,
			}))
			break
		}
	}
	return JSONResult(resp, suggestions...)
}

// HandleUserErrors returns the error groups one user has hit.
func HandleUserErrors(ctx context.Context, d UsersDeps, args map[string]any) (*CallToolResult, error) {
	if _, err := ResolveEnv(ctx, args); err != nil {
		return NewToolResultError(err.Error()), nil
	}
	if d.ErrorImpactStore == nil {
		return NewToolResultError("error impact tracking is unavailable"), nil
	}

	userID := ArgString(args, "user_id")
	if userID == "" {
		return NewToolResultError("user_id is required"), nil
	}

	since, window, err := ResolveWindow(args, "7d")
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	summaries, err := d.ErrorImpactStore.GetUserErrors(ctx, userID, since)
	if err != nil {
		return NewToolResultError("reading this user's errors failed"), nil
	}
	if limit := usersLimit(args); len(summaries) > limit {
		summaries = summaries[:limit]
	}

	resp := map[string]any{
		"user_id": userID,
		"window":  window,
		"since":   since.Format(time.RFC3339),
		"count":   len(summaries),
		"errors":  summaries,
	}
	if len(summaries) == 0 {
		resp["summary"] = fmt.Sprintf("No errors for %q in the last %s.", userID, window)
		return JSONResult(resp)
	}
	return JSONResult(resp, Suggest("errors", "Investigate the first error", map[string]any{
		"action": "detail", "fingerprint": summaries[0].Fingerprint,
	}))
}

// HandleUserImpact answers the other direction: who is this error hurting.
func HandleUserImpact(ctx context.Context, d UsersDeps, args map[string]any) (*CallToolResult, error) {
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	if d.ErrorImpactStore == nil {
		return NewToolResultError("error impact tracking is unavailable"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required"), nil
	}

	// Check the group exists in the caller's scope first. Without this, an
	// env-scoped token could enumerate the users affected by an error that
	// belongs to an environment it cannot otherwise read.
	if d.ErrorGroupStore != nil {
		if _, err := d.ErrorGroupStore.Get(ctx, fingerprint, env); err != nil {
			return NewToolResultError("no such error group in this environment"), nil
		}
	}

	resp := map[string]any{"fingerprint": fingerprint}
	if env != "" {
		resp["environment"] = env
	}

	if impact, err := d.ErrorImpactStore.GetImpact(ctx, fingerprint, env); err == nil && impact != nil {
		resp["unique_users"] = impact.UniqueUsers
		resp["total_occurrences"] = impact.TotalOccurrences
		resp["impact_score"] = impact.ImpactScore
		if len(impact.CommonTraits) > 0 {
			resp["common_traits"] = impact.CommonTraits
		}
	}

	users, err := d.ErrorImpactStore.GetAffectedUsers(ctx, fingerprint, usersLimit(args))
	if err != nil {
		return NewToolResultError("reading affected users failed"), nil
	}
	resp["affected_users"] = users
	resp["count"] = len(users)

	if len(users) == 0 {
		resp["summary"] = "No individual users are recorded for this error — it may predate impact tracking, or affect anonymous traffic."
		return JSONResult(resp)
	}
	return JSONResult(resp, Suggest("users", "What else happened to the worst-affected user", map[string]any{
		"action": "timeline", "user_id": users[0].UserID,
	}))
}

// usersLimit reads the caller's limit, defaulted and capped. An uncapped limit
// here is an unbounded scan over every segment on disk.
func usersLimit(args map[string]any) int {
	return ArgInt(args, "limit", defaultUsersLimit, maxUsersLimit)
}
