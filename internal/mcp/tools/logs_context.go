package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: context — surrounding log entries around a log ID (from logContextHandler)
// ---------------------------------------------------------------------------

func LogsContext(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	logID, ok := args["log_id"].(float64)
	if !ok || logID <= 0 {
		return NewToolResultError("log_id is required (positive integer)"), nil
	}

	before := 10
	if v, ok := args["before"].(float64); ok && v >= 0 {
		before = int(v)
		if before > 50 {
			before = 50
		}
	}
	after := 10
	if v, ok := args["after"].(float64); ok && v >= 0 {
		after = int(v)
		if after > 50 {
			after = 50
		}
	}
	sameService := ArgBool(args, "same_service")

	// Fetch the anchor log entry.
	anchor, err := deps.LogStore.GetByID(ctx, int64(logID))
	if err != nil {
		return NewToolResultError(fmt.Sprintf("log entry %d not found: %v", int64(logID), err)), nil
	}

	// Fetch entries before (older timestamps, i.e. timestamp < anchor, order DESC, take `before`).
	beforeParams := store.LogSearchParams{
		End:     &anchor.Timestamp,
		Limit:   before,
		SortAsc: false, // newest first so we get the closest entries
	}
	if sameService && anchor.Service != "" {
		beforeParams.Service = anchor.Service
	}
	beforeEntries, err := deps.LogStore.Search(ctx, beforeParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to fetch context before: %v", err)), nil
	}

	// Filter out the anchor entry itself from before results.
	filtered := make([]store.LogEntry, 0, len(beforeEntries))
	for _, e := range beforeEntries {
		if e.ID != anchor.ID {
			filtered = append(filtered, e)
		}
	}
	beforeEntries = filtered

	// Reverse so they're oldest-first.
	for i, j := 0, len(beforeEntries)-1; i < j; i, j = i+1, j-1 {
		beforeEntries[i], beforeEntries[j] = beforeEntries[j], beforeEntries[i]
	}

	// Fetch entries after (newer timestamps, i.e. timestamp > anchor, order ASC, take `after`).
	afterParams := store.LogSearchParams{
		Start:   &anchor.Timestamp,
		Limit:   after + 1, // +1 because anchor might be included
		SortAsc: true,
	}
	if sameService && anchor.Service != "" {
		afterParams.Service = anchor.Service
	}
	afterEntries, err := deps.LogStore.Search(ctx, afterParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to fetch context after: %v", err)), nil
	}

	// Filter out the anchor entry itself.
	filteredAfter := make([]store.LogEntry, 0, len(afterEntries))
	for _, e := range afterEntries {
		if e.ID != anchor.ID {
			filteredAfter = append(filteredAfter, e)
		}
	}
	afterEntries = filteredAfter
	if len(afterEntries) > after {
		afterEntries = afterEntries[:after]
	}

	// Build response.
	type contextEntry struct {
		ID               int64          `json:"id"`
		Timestamp        string         `json:"timestamp"`
		Level            string         `json:"level"`
		Service          string         `json:"service,omitempty"`
		Message          string         `json:"message"`
		ExceptionClass   string         `json:"exception_class,omitempty"`
		ErrorFingerprint string         `json:"error_fingerprint,omitempty"`
		SourceFile       string         `json:"source_file,omitempty"`
		SourceLine       int            `json:"source_line,omitempty"`
		TraceID          string         `json:"trace_id,omitempty"`
		RequestID        string         `json:"request_id,omitempty"`
		Metadata         map[string]any `json:"metadata,omitempty"`
		Position         string         `json:"position"` // "before", "anchor", "after"
	}

	toContextEntry := func(e store.LogEntry, pos string, truncMsg bool) contextEntry {
		msg := e.Message
		if truncMsg && len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return contextEntry{
			ID:               e.ID,
			Timestamp:        e.Timestamp.Format(time.RFC3339Nano),
			Level:            e.Level,
			Service:          e.Service,
			Message:          msg,
			ExceptionClass:   e.ExceptionClass,
			ErrorFingerprint: e.ErrorFingerprint,
			SourceFile:       e.SourceFile,
			SourceLine:       e.SourceLine,
			TraceID:          e.TraceID,
			RequestID:        e.RequestID,
			Metadata:         e.Metadata,
			Position:         pos,
		}
	}

	ctxEntries := make([]contextEntry, 0, len(beforeEntries)+1+len(afterEntries))
	for _, e := range beforeEntries {
		ctxEntries = append(ctxEntries, toContextEntry(e, "before", true))
	}
	ctxEntries = append(ctxEntries, toContextEntry(*anchor, "anchor", false))
	for _, e := range afterEntries {
		ctxEntries = append(ctxEntries, toContextEntry(e, "after", true))
	}

	resp := map[string]any{
		"anchor_id":    int64(logID),
		"same_service": sameService,
		"before_count": len(beforeEntries),
		"after_count":  len(afterEntries),
		"entries":      ctxEntries,
	}

	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// action: attributes — discover distinct values for log fields (from listLogAttributesHandler)
// ---------------------------------------------------------------------------

func LogsAttributes(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	field := ArgString(args, "field")
	if field == "" {
		return NewToolResultError("field is required (service, level, event_type, environment, commit_hash, request_id, exception_class, error_fingerprint, source_file, or metadata_key)"), nil
	}

	// Parse time range (default: 24h).
	timeRange := ArgStringDefault(args, "time_range", "24h")
	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
	}
	now := time.Now().UTC()
	params := store.LogCountParams{
		Since: now.Add(-duration),
		Until: now,
	}
	params.Service = ArgString(args, "service")

	if field == "metadata_key" {
		keys, err := deps.LogStore.MetadataKeys(ctx, params)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to list metadata keys: %v", err)), nil
		}
		if len(keys) == 0 {
			return EmptyResult("No metadata keys found in the specified time range.")
		}
		return JSONResult(map[string]any{
			"field":  "metadata_key",
			"count":  len(keys),
			"values": keys,
			"hint":   "Use these keys with the metadata_filter parameter in log_search (e.g. metadata_filter: {\"host\": \"server-01\"}).",
		})
	}

	values, err := deps.LogStore.DistinctValues(ctx, field, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list values: %v", err)), nil
	}

	if len(values) == 0 {
		return EmptyResult(fmt.Sprintf("No %s values found in the specified time range.", field))
	}

	return JSONResult(map[string]any{
		"field":  field,
		"count":  len(values),
		"values": values,
	})
}
