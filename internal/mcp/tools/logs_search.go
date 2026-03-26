package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: search — full-text log search with filters (from logSearchHandler)
// ---------------------------------------------------------------------------

func logsSearch(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	query, _ := args["query"].(string)
	service, _ := args["service"].(string)
	level, _ := args["level"].(string)
	traceID, _ := args["trace_id"].(string)
	eventType, _ := args["event_type"].(string)
	commitHash, _ := args["commit_hash"].(string)
	requestID, _ := args["request_id"].(string)
	environment, _ := args["environment"].(string)
	exceptionClass, _ := args["exception_class"].(string)
	errorFingerprint, _ := args["error_fingerprint"].(string)
	sourceFile, _ := args["source_file"].(string)

	limit := 50
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
		if limit > 200 {
			limit = 200
		}
	}

	offset := 0
	if v, ok := args["offset"].(float64); ok && v > 0 {
		offset = int(v)
	}

	// Sort order (default: desc = newest first).
	sortAsc := false
	if v, ok := args["sort"].(string); ok && v == "asc" {
		sortAsc = true
	}

	// Fields projection.
	var fields map[string]bool
	if v, ok := args["fields"].(string); ok && v != "" {
		fields = make(map[string]bool)
		for _, f := range strings.Split(v, ",") {
			fields[strings.TrimSpace(f)] = true
		}
	}

	// Metadata filter.
	var metadataFilter map[string]string
	if v, ok := args["metadata_filter"].(map[string]any); ok && len(v) > 0 {
		metadataFilter = make(map[string]string, len(v))
		for k, val := range v {
			metadataFilter[k] = fmt.Sprintf("%v", val)
		}
	}

	params := store.LogSearchParams{
		Query:            query,
		Service:          service,
		Level:            level,
		Environment:      environment,
		CommitHash:       commitHash,
		TraceID:          traceID,
		RequestID:        requestID,
		EventType:        eventType,
		ExceptionClass:   exceptionClass,
		ErrorFingerprint: errorFingerprint,
		SourceFile:       sourceFile,
		Limit:            limit,
		Offset:           offset,
		SortAsc:          sortAsc,
		MetadataFilter:   metadataFilter,
	}

	// Parse time range.
	if v, ok := args["time_range"].(string); ok && v != "" {
		duration, err := parseTimeRange(v)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v. Use formats like '15m', '1h', '6h', '24h', '7d'.", err)), nil
		}
		now := time.Now().UTC()
		start := now.Add(-duration)
		params.Start = &start
		params.End = &now
	}

	entries, err := deps.LogStore.Search(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to search logs: %v. Verify your query syntax and filters.", err)), nil
	}

	// If FTS query returned nothing, try a fallback LIKE search against
	// the service field (expanded search scope).
	if len(entries) == 0 && query != "" && service == "" {
		fallbackParams := params
		fallbackParams.Query = ""
		fallbackParams.Service = query
		entries, _ = deps.LogStore.Search(ctx, fallbackParams)
	}

	if len(entries) == 0 {
		hint := "No log entries found matching your criteria."
		if query != "" {
			hint += " Try broadening your search query or extending the time_range."
		}
		if level != "" {
			hint += fmt.Sprintf(" Level filter '%s' is active — try removing it.", level)
		}
		return mcp.NewToolResultText(hint), nil
	}

	// Pre-fetch error group info for entries with fingerprints.
	errorGroupCache := make(map[string]*store.ErrorGroup)
	if deps.ErrorGroupStore != nil {
		seen := make(map[string]bool)
		for _, e := range entries {
			if e.ErrorFingerprint != "" && !seen[e.ErrorFingerprint] {
				seen[e.ErrorFingerprint] = true
				if eg, err := deps.ErrorGroupStore.Get(ctx, e.ErrorFingerprint); err == nil {
					errorGroupCache[e.ErrorFingerprint] = eg
				}
			}
		}
	}

	// Build response entries with optional field projection.
	results := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		msg := e.Message
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}

		entry := make(map[string]any)
		if fields == nil || fields["id"] {
			entry["id"] = e.ID
		}
		if fields == nil || fields["timestamp"] {
			entry["timestamp"] = e.Timestamp.Format(time.RFC3339Nano)
		}
		if fields == nil || fields["level"] {
			entry["level"] = e.Level
		}
		if (fields == nil || fields["service"]) && e.Service != "" {
			entry["service"] = e.Service
		}
		if (fields == nil || fields["environment"]) && e.Environment != "" {
			entry["environment"] = e.Environment
		}
		if (fields == nil || fields["commit_hash"]) && e.CommitHash != "" {
			entry["commit_hash"] = e.CommitHash
		}
		if (fields == nil || fields["trace_id"]) && e.TraceID != "" {
			entry["trace_id"] = e.TraceID
		}
		if (fields == nil || fields["request_id"]) && e.RequestID != "" {
			entry["request_id"] = e.RequestID
		}
		if fields == nil || fields["message"] {
			entry["message"] = msg
		}
		if (fields == nil || fields["event_type"]) && e.EventType != "" {
			entry["event_type"] = e.EventType
		}
		if (fields == nil || fields["exception_class"]) && e.ExceptionClass != "" {
			entry["exception_class"] = e.ExceptionClass
		}
		if (fields == nil || fields["error_fingerprint"]) && e.ErrorFingerprint != "" {
			entry["error_fingerprint"] = e.ErrorFingerprint
		}
		if (fields == nil || fields["source_file"]) && e.SourceFile != "" {
			entry["source_file"] = e.SourceFile
			if e.SourceLine > 0 {
				entry["source_line"] = e.SourceLine
			}
		}
		if (fields == nil || fields["metadata"]) && len(e.Metadata) > 0 {
			entry["metadata"] = e.Metadata
		}
		// Enrich with error group context when available.
		if e.ErrorFingerprint != "" {
			if eg, ok := errorGroupCache[e.ErrorFingerprint]; ok {
				entry["error_group"] = map[string]any{
					"status":           string(eg.Status),
					"occurrence_count": eg.OccurrenceCount,
					"reopened_count":   eg.ReopenedCount,
					"last_seen_at":     eg.LastSeenAt.Format(time.RFC3339),
				}
			}
		}
		results = append(results, entry)
	}

	// Build a compact one-line-per-entry text summary so the AI agent
	// can quickly scan results without parsing full JSON.
	summaryLines := make([]string, 0, len(entries))
	for _, e := range entries {
		ts := e.Timestamp.Format("2006-01-02 15:04:05")
		msg := e.Message
		if len(msg) > 120 {
			msg = msg[:120] + "..."
		}

		var b strings.Builder
		b.Grow(128)
		b.WriteString("[")
		b.WriteString(ts)
		b.WriteString("] ")
		b.WriteString(e.Level)
		b.WriteString(" [")
		b.WriteString(e.Service)
		b.WriteString("]")

		// Surface exception info prominently in the summary line.
		if e.ExceptionClass != "" {
			b.WriteString(" ")
			b.WriteString(e.ExceptionClass)
			if e.SourceFile != "" {
				b.WriteString(" at ")
				b.WriteString(e.SourceFile)
				if e.SourceLine > 0 {
					b.WriteString(":")
					b.WriteString(strconv.Itoa(e.SourceLine))
				}
			}
			// Include exception_message from metadata if available.
			if meta, ok := e.Metadata["exception_message"]; ok {
				excMsg := fmt.Sprintf("%v", meta)
				if len(excMsg) > 100 {
					excMsg = excMsg[:100] + "..."
				}
				b.WriteString(": ")
				b.WriteString(excMsg)
			}
		} else {
			b.WriteString(": ")
			b.WriteString(msg)
		}

		// Append trace_id if present for easy correlation.
		if e.TraceID != "" {
			b.WriteString(" trace=")
			b.WriteString(e.TraceID)
		}

		summaryLines = append(summaryLines, b.String())
	}

	resp := map[string]any{
		"total_returned": len(results),
		"text_summary":   strings.Join(summaryLines, "\n"),
		"entries":        results,
	}

	if len(results) == limit {
		resp["has_more"] = true
		resp["next_offset"] = offset + limit
		resp["hint"] = "More results may be available. Use the 'offset' parameter to paginate."
	}

	// Summary: distribution of returned entries by level and service.
	levelDist := make(map[string]int)
	serviceDist := make(map[string]int)
	for _, e := range entries {
		levelDist[e.Level]++
		if e.Service != "" {
			serviceDist[e.Service]++
		}
	}
	if len(entries) > 1 {
		resp["summary"] = map[string]any{
			"by_level":   levelDist,
			"by_service": serviceDist,
		}
	}

	// Time histogram: bucket results by time to show distribution.
	if len(entries) > 1 {
		resp["time_distribution"] = logsBuildTimeHistogram(entries)
	}

	// Suggest next steps based on results.
	var suggestions []ToolSuggestion
	if len(entries) > 0 {
		// For error entries, suggest investigate_error for one-call deep dive.
		for _, e := range entries {
			if e.Level == "ERROR" || e.Level == "FATAL" {
				suggestions = append(suggestions, Suggest("investigate_error", "Deep-dive into this error: exception, backtrace, params, SQL, context", map[string]any{
					"log_id": e.ID,
				}))
				break
			}
		}
		// Suggest log_context for the first result that has an ID.
		if entries[0].ID > 0 {
			suggestions = append(suggestions, Suggest("log_context", "See surrounding logs for this entry", map[string]any{
				"log_id": entries[0].ID,
			}))
		}
		// If there's an error fingerprint, suggest error_detail.
		for _, e := range entries {
			if e.ErrorFingerprint != "" {
				suggestions = append(suggestions, Suggest("error_detail", "Investigate this error group", map[string]any{
					"fingerprint": e.ErrorFingerprint,
				}))
				break
			}
		}
		// If there's a trace ID, suggest trace_lookup.
		for _, e := range entries {
			if e.TraceID != "" {
				suggestions = append(suggestions, Suggest("trace_lookup", "Follow distributed trace", map[string]any{
					"trace_id": e.TraceID,
				}))
				break
			}
		}
	}
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// action: context — surrounding log entries around a log ID (from logContextHandler)
// ---------------------------------------------------------------------------

func logsContext(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	logID, ok := args["log_id"].(float64)
	if !ok || logID <= 0 {
		return mcp.NewToolResultError("log_id is required (positive integer)"), nil
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

	sameService := false
	if v, ok := args["same_service"].(bool); ok {
		sameService = v
	}

	// Fetch the anchor log entry.
	anchor, err := deps.LogStore.GetByID(ctx, int64(logID))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("log entry %d not found: %v", int64(logID), err)), nil
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
		return mcp.NewToolResultError(fmt.Sprintf("failed to fetch context before: %v", err)), nil
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
		return mcp.NewToolResultError(fmt.Sprintf("failed to fetch context after: %v", err)), nil
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

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// action: attributes — discover distinct values for log fields (from listLogAttributesHandler)
// ---------------------------------------------------------------------------

func logsAttributes(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	field, _ := args["field"].(string)
	if field == "" {
		return mcp.NewToolResultError("field is required (service, level, event_type, environment, commit_hash, request_id, exception_class, error_fingerprint, source_file, or metadata_key)"), nil
	}

	// Parse time range (default: 24h).
	timeRange := "24h"
	if v, ok := args["time_range"].(string); ok && v != "" {
		timeRange = v
	}
	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
	}
	now := time.Now().UTC()
	params := store.LogCountParams{
		Since: now.Add(-duration),
		Until: now,
	}
	if v, ok := args["service"].(string); ok && v != "" {
		params.Service = v
	}

	if field == "metadata_key" {
		keys, err := deps.LogStore.MetadataKeys(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list metadata keys: %v", err)), nil
		}
		if len(keys) == 0 {
			return mcp.NewToolResultText("No metadata keys found in the specified time range."), nil
		}
		resp := map[string]any{
			"field":  "metadata_key",
			"count":  len(keys),
			"values": keys,
			"hint":   "Use these keys with the metadata_filter parameter in log_search (e.g. metadata_filter: {\"host\": \"server-01\"}).",
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}

	values, err := deps.LogStore.DistinctValues(ctx, field, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list values: %v", err)), nil
	}

	if len(values) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No %s values found in the specified time range.", field)), nil
	}

	resp := map[string]any{
		"field":  field,
		"count":  len(values),
		"values": values,
	}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}
