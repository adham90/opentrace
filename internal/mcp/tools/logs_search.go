package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: search — full-text log search with filters (from logSearchHandler)
// ---------------------------------------------------------------------------

func LogsSearch(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	query := ArgString(args, "query")
	service := ArgString(args, "service")
	level := ArgString(args, "level")
	traceID := ArgString(args, "trace_id")
	eventType := ArgString(args, "event_type")
	commitHash := ArgString(args, "commit_hash")
	requestID := ArgString(args, "request_id")
	environment := ArgString(args, "environment")
	exceptionClass := ArgString(args, "exception_class")
	errorFingerprint := ArgString(args, "error_fingerprint")
	sourceFile := ArgString(args, "source_file")
	limit := ArgInt(args, "limit", 50, 200)
	offset := ArgInt(args, "offset", 0, 100000)

	// Sort order (default: desc = newest first).
	sortAsc := ArgString(args, "sort") == "asc"

	// Fields projection.
	var fields map[string]bool
	if v := ArgString(args, "fields"); v != "" {
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
	if v := ArgString(args, "time_range"); v != "" {
		duration, err := ParseTimeRange(v)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("invalid time_range: %v. Use formats like '15m', '1h', '6h', '24h', '7d'.", err)), nil
		}
		now := time.Now().UTC()
		start := now.Add(-duration)
		params.Start = &start
		params.End = &now
	}

	entries, err := deps.LogStore.Search(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to search logs: %v. Verify your query syntax and filters.", err)), nil
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
		return NewToolResultText(hint), nil
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
				suggestions = append(suggestions, Suggest("errors", "Deep-dive into this error: exception, backtrace, params, SQL, context", map[string]any{
					"action": "investigate",
					"log_id": e.ID,
				}))
				break
			}
		}
		// Suggest log_context for the first result that has an ID.
		if entries[0].ID > 0 {
			suggestions = append(suggestions, Suggest("logs", "See surrounding logs for this entry", map[string]any{
				"action": "context",
				"log_id": entries[0].ID,
			}))
		}
		// If there's an error fingerprint, suggest error_detail.
		for _, e := range entries {
			if e.ErrorFingerprint != "" {
				suggestions = append(suggestions, Suggest("errors", "Investigate this error group", map[string]any{
					"action":      "detail",
					"fingerprint": e.ErrorFingerprint,
				}))
				break
			}
		}
		// If there's a trace ID, suggest trace_lookup.
		for _, e := range entries {
			if e.TraceID != "" {
				suggestions = append(suggestions, Suggest("logs", "Follow distributed trace", map[string]any{
					"action":   "trace",
					"trace_id": e.TraceID,
				}))
				break
			}
		}
	}
	return JSONResultRanked(resp, deps.Ranker, suggestions...)
}
