package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// TraceSessionRecorder is an optional callback for recording trace IDs
// discovered during trace lookups into the current investigation session.
type TraceSessionRecorder func(traceID string)

// LogsDeps holds the dependencies for the consolidated logs tool.
type LogsDeps struct {
	LogStore             store.LogStore
	ErrorGroupStore      store.ErrorGroupStore
	TraceSessionRecorder TraceSessionRecorder // optional, nil-safe
	Ranker               SuggestionRanker     // optional, nil-safe
}

// LogsCatalogInfo returns the category, description, and access level for catalog registration.
func LogsCatalogInfo() (category, description, access string) {
	return "Log Intelligence",
		"Unified log intelligence: search, context, attributes, stats, summary, performance, trace, compare",
		"read"
}

// LogsTool returns the MCP tool definition for the consolidated logs tool.
func LogsTool() mcp.Tool {
	return mcp.NewTool("logs",
		mcp.WithDescription("Unified log intelligence tool. Use 'action' to select the operation: "+
			"search (full-text log search with filters), "+
			"context (surrounding entries around a log ID), "+
			"attributes (discover distinct field values), "+
			"stats (aggregate statistics by level/service/pattern), "+
			"summary (debugging overview: error rates, deploys, top errors, slow endpoints), "+
			"performance (request performance: N+1 queries, slow endpoints), "+
			"trace (distributed trace assembly by trace ID), "+
			"compare (compare metrics between two time periods)."),

		// -- shared parameter --
		mcp.WithString("action", mcp.Required(),
			mcp.Description("Operation to perform: search, context, attributes, stats, summary, performance, trace, compare")),

		// -- search parameters --
		mcp.WithString("query",
			mcp.Description("[search] Full-text search query (searches message content). Also tries matching service names if no FTS results found.")),
		mcp.WithString("service",
			mcp.Description("[search/attributes/stats/summary/compare] Filter by service name")),
		mcp.WithString("level",
			mcp.Description("[search/stats] Filter by log level: debug, info, warn, error, fatal (comma-separated for multiple)")),
		mcp.WithString("environment",
			mcp.Description("[search/summary] Filter by deployment environment (e.g. production, staging, development)")),
		mcp.WithString("commit_hash",
			mcp.Description("[search/summary] Filter by git commit hash. Supports short hashes (prefix match) and full 40-char SHA.")),
		mcp.WithString("trace_id",
			mcp.Description("[search/trace] Filter by trace/correlation ID")),
		mcp.WithString("request_id",
			mcp.Description("[search] Filter by request ID to see all logs from a single HTTP request")),
		mcp.WithString("event_type",
			mcp.Description("[search] Filter by event type (e.g. payment.completed, auth.login)")),
		mcp.WithString("exception_class",
			mcp.Description("[search] Filter by exception class name (e.g. NoMethodError, ActiveRecord::RecordNotFound)")),
		mcp.WithString("error_fingerprint",
			mcp.Description("[search] Filter by error fingerprint to find all occurrences of the same error")),
		mcp.WithString("source_file",
			mcp.Description("[search] Filter by source file path (e.g. app/models/user.rb). Partial match supported.")),
		mcp.WithString("time_range",
			mcp.Description("[search/attributes/stats/summary/performance] Lookback window: '15m', '1h', '6h', '24h', '7d'")),
		mcp.WithNumber("limit",
			mcp.Description("[search/performance] Maximum entries to return (search default: 50 max: 200, performance default: 20 max: 100)")),
		mcp.WithNumber("offset",
			mcp.Description("[search] Skip this many entries for pagination (default: 0)")),
		mcp.WithString("sort",
			mcp.Description("[search] Sort order: 'desc' (default, newest first) or 'asc' (oldest first)")),
		mcp.WithString("fields",
			mcp.Description("[search] Comma-separated list of fields to include (e.g. 'timestamp,level,message'). Omit for all fields.")),
		mcp.WithObject("metadata_filter",
			mcp.Description("[search] Key-value filter on metadata fields. Exact: {\"host\": \"server-01\"}, contains: {\"host\": \"~server\"}, exists: {\"host\": \"*\"}")),

		// -- context parameters --
		mcp.WithNumber("log_id",
			mcp.Description("[context] Log entry ID (from search results)")),
		mcp.WithNumber("before",
			mcp.Description("[context] Number of entries before the anchor (default: 10, max: 50)")),
		mcp.WithNumber("after",
			mcp.Description("[context] Number of entries after the anchor (default: 10, max: 50)")),
		mcp.WithBoolean("same_service",
			mcp.Description("[context] Only show entries from the same service as the anchor (default: false)")),

		// -- attributes parameters --
		mcp.WithString("field",
			mcp.Description("[attributes] Field to list values for: service, level, event_type, environment, commit_hash, request_id, exception_class, error_fingerprint, source_file, metadata_key")),

		// -- stats parameters --
		mcp.WithString("group_by",
			mcp.Description("[stats] Primary grouping: 'level' (default), 'service', 'pattern' (clusters similar error messages)")),
		mcp.WithString("bucket_interval",
			mcp.Description("[stats] Time bucket size for trend data: '1m', '5m' (default), '15m', '1h'")),

		// -- performance parameters --
		mcp.WithString("controller",
			mcp.Description("[performance] Filter by controller name (partial match)")),
		mcp.WithString("path",
			mcp.Description("[performance] Filter by request path (partial match)")),
		mcp.WithBoolean("n_plus_one_only",
			mcp.Description("[performance] Only show requests with N+1 query issues (default: false)")),
		mcp.WithNumber("min_duration_ms",
			mcp.Description("[performance] Minimum request duration in milliseconds")),
		mcp.WithNumber("min_sql_count",
			mcp.Description("[performance] Minimum number of SQL queries in request")),
		mcp.WithString("sort_by",
			mcp.Description("[performance] Sort by: 'duration_ms' (default), 'sql_count', 'db_time_ms', 'duplicate_queries'")),

		// -- trace parameters --
		mcp.WithBoolean("include_context",
			mcp.Description("[trace] Include surrounding log entries (+/- 2 seconds) from each service (default: false)")),

		// -- compare parameters --
		mcp.WithString("metric",
			mcp.Description("[compare] What to compare: 'errors' (log error rates), 'log_volume' (total log counts by level)")),
		mcp.WithString("current_period",
			mcp.Description("[compare] Current period: 'last_1h' (default), 'last_6h', 'last_24h', 'today'")),
		mcp.WithString("baseline_period",
			mcp.Description("[compare] Baseline to compare against: 'previous' (default), 'yesterday_same_time', 'last_week_same_time'")),
	)
}

// LogsHandler returns a handler that dispatches to the appropriate action.
func LogsHandler(deps LogsDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		action, _ := args["action"].(string)
		switch action {
		case "search":
			return logsSearch(ctx, args, deps)
		case "context":
			return logsContext(ctx, args, deps)
		case "attributes":
			return logsAttributes(ctx, args, deps)
		case "stats":
			return logsStats(ctx, args, deps)
		case "summary":
			return logsSummary(ctx, args, deps)
		case "performance":
			return logsPerformance(ctx, args, deps)
		case "trace":
			return logsTrace(ctx, args, deps)
		case "compare":
			return logsCompare(ctx, args, deps)
		default:
			return mcp.NewToolResultError(
				"action is required and must be one of: search, context, attributes, stats, summary, performance, trace, compare",
			), nil
		}
	}
}

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

// ---------------------------------------------------------------------------
// action: stats — aggregate log statistics (from logStatsHandler)
// ---------------------------------------------------------------------------

func logsStats(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	timeRange := "1h"
	if v, ok := args["time_range"].(string); ok && v != "" {
		timeRange = v
	}

	groupBy := "level"
	if v, ok := args["group_by"].(string); ok && v != "" {
		groupBy = v
	}

	serviceFilter, _ := args["service"].(string)
	levelFilter, _ := args["level"].(string)

	bucketInterval := "5m"
	if v, ok := args["bucket_interval"].(string); ok && v != "" {
		bucketInterval = v
	}

	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
	}

	bucketDur, err := parseTimeRange(bucketInterval)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid bucket_interval: %v", err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	params := store.LogCountParams{
		Since:   since,
		Until:   now,
		Service: serviceFilter,
		Level:   levelFilter,
	}

	switch groupBy {
	case "level":
		return logsStatsByLevel(ctx, deps.LogStore, params, since, now, bucketDur)
	case "service":
		return logsStatsByService(ctx, deps.LogStore, params, since, now)
	case "pattern":
		return logsStatsByPattern(ctx, deps.LogStore, since, now, serviceFilter)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("invalid group_by: %q (use level, service, or pattern)", groupBy)), nil
	}
}

func logsStatsByLevel(ctx context.Context, ls store.LogStore, params store.LogCountParams, since, until time.Time, bucketDur time.Duration) (*mcp.CallToolResult, error) {
	counts, err := ls.CountByLevel(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
	}

	total := 0
	for _, c := range counts {
		total += c
	}

	errorCount := counts["error"] + counts["fatal"]
	var errorRate float64
	if total > 0 {
		errorRate = float64(errorCount) / float64(total) * 100
	}

	// Build trend buckets.
	var trend []map[string]any
	for t := since; t.Before(until); t = t.Add(bucketDur) {
		bucketEnd := t.Add(bucketDur)
		if bucketEnd.After(until) {
			bucketEnd = until
		}
		bucketParams := store.LogCountParams{
			Since:   t,
			Until:   bucketEnd,
			Service: params.Service,
			Level:   params.Level,
		}
		bucketCounts, err := ls.CountByLevel(ctx, bucketParams)
		if err != nil {
			continue
		}
		bucket := map[string]any{
			"bucket": t.Format(time.RFC3339),
		}
		for level, count := range bucketCounts {
			bucket[level] = count
		}
		trend = append(trend, bucket)
	}

	var warnings []string
	// Detect error rate spike in last bucket vs average.
	if len(trend) >= 2 {
		lastBucket := trend[len(trend)-1]
		lastErrors := logsToInt(lastBucket["error"]) + logsToInt(lastBucket["fatal"])
		avgErrors := float64(errorCount) / float64(len(trend))
		if avgErrors > 0 && float64(lastErrors) > avgErrors*1.4 {
			pctIncrease := (float64(lastErrors) - avgErrors) / avgErrors * 100
			warnings = append(warnings, fmt.Sprintf("Error rate increased %.0f%% in the last bucket compared to the average", pctIncrease))
		}
	}

	resp := map[string]any{
		"time_range":     map[string]any{"start": since.Format(time.RFC3339), "end": until.Format(time.RFC3339)},
		"total_logs":     total,
		"by_level":       counts,
		"error_rate_pct": logsRound2(errorRate),
	}
	if len(trend) > 0 {
		resp["trend"] = trend
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func logsStatsByService(ctx context.Context, ls store.LogStore, params store.LogCountParams, since, until time.Time) (*mcp.CallToolResult, error) {
	services, err := ls.CountByService(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
	}

	total := 0
	for _, s := range services {
		total += s.Total
	}

	var byService []map[string]any
	var avgErrorRate float64
	totalErrors := 0
	for _, s := range services {
		var errRate float64
		if s.Total > 0 {
			errRate = float64(s.ErrorCount) / float64(s.Total) * 100
		}
		totalErrors += s.ErrorCount
		byService = append(byService, map[string]any{
			"service":        s.Service,
			"total":          s.Total,
			"errors":         s.ErrorCount,
			"error_rate_pct": logsRound2(errRate),
		})
	}
	if total > 0 {
		avgErrorRate = float64(totalErrors) / float64(total) * 100
	}

	var warnings []string
	for _, s := range services {
		if s.Total > 0 {
			rate := float64(s.ErrorCount) / float64(s.Total) * 100
			if avgErrorRate > 0 && rate > avgErrorRate*1.25 {
				warnings = append(warnings, fmt.Sprintf("Service '%s' error rate (%.1f%%) is above average (%.1f%%)", s.Service, rate, avgErrorRate))
			}
		}
	}

	resp := map[string]any{
		"time_range": map[string]any{"start": since.Format(time.RFC3339), "end": until.Format(time.RFC3339)},
		"total_logs": total,
		"by_service": byService,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func logsStatsByPattern(ctx context.Context, ls store.LogStore, since, until time.Time, service string) (*mcp.CallToolResult, error) {
	// Fetch error/fatal logs for pattern clustering.
	searchParams := store.LogSearchParams{
		Level:   "error",
		Service: service,
		Start:   &since,
		End:     &until,
		Limit:   10000,
	}

	errorLogs, err := ls.Search(ctx, searchParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
	}

	// Also fetch fatal logs.
	searchParams.Level = "fatal"
	fatalLogs, err := ls.Search(ctx, searchParams)
	if err == nil {
		errorLogs = append(errorLogs, fatalLogs...)
	}

	// Cluster by normalized message.
	type patternData struct {
		normalized string
		count      int
		firstSeen  time.Time
		lastSeen   time.Time
		sample     string
		services   map[string]bool
	}

	patterns := make(map[string]*patternData)
	for _, logEntry := range errorLogs {
		norm := logsNormalizeMessage(logEntry.Message)
		p, ok := patterns[norm]
		if !ok {
			p = &patternData{
				normalized: norm,
				firstSeen:  logEntry.Timestamp,
				lastSeen:   logEntry.Timestamp,
				sample:     logEntry.Message,
				services:   make(map[string]bool),
			}
			patterns[norm] = p
		}
		p.count++
		if logEntry.Timestamp.Before(p.firstSeen) {
			p.firstSeen = logEntry.Timestamp
		}
		if logEntry.Timestamp.After(p.lastSeen) {
			p.lastSeen = logEntry.Timestamp
		}
		if logEntry.Service != "" {
			p.services[logEntry.Service] = true
		}
	}

	// Sort by count descending.
	type sortablePattern struct {
		key string
		pd  *patternData
	}
	var sorted []sortablePattern
	for k, v := range patterns {
		sorted = append(sorted, sortablePattern{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].pd.count > sorted[j].pd.count
	})

	totalErrors := len(errorLogs)
	limit := 20
	if len(sorted) < limit {
		limit = len(sorted)
	}

	var patternEntries []map[string]any
	for _, sp := range sorted[:limit] {
		var svcs []string
		for s := range sp.pd.services {
			svcs = append(svcs, s)
		}
		sort.Strings(svcs)

		var pctOfErrors float64
		if totalErrors > 0 {
			pctOfErrors = float64(sp.pd.count) / float64(totalErrors) * 100
		}

		entry := map[string]any{
			"pattern":        sp.key,
			"count":          sp.pd.count,
			"pct_of_errors":  logsRound2(pctOfErrors),
			"first_seen":     sp.pd.firstSeen.Format(time.RFC3339),
			"last_seen":      sp.pd.lastSeen.Format(time.RFC3339),
			"sample_message": sp.pd.sample,
		}
		if len(svcs) > 0 {
			entry["services"] = svcs
		}
		patternEntries = append(patternEntries, entry)
	}

	var warnings []string
	if len(sorted) > 0 && totalErrors > 0 {
		topPct := float64(sorted[0].pd.count) / float64(totalErrors) * 100
		if topPct > 50 {
			warnings = append(warnings, fmt.Sprintf("Top error pattern '%s' accounts for %.0f%% of all errors", sorted[0].key, topPct))
		}
	}

	resp := map[string]any{
		"time_range":   map[string]any{"start": since.Format(time.RFC3339), "end": until.Format(time.RFC3339)},
		"total_errors": totalErrors,
		"patterns":     patternEntries,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// action: summary — debugging overview (from logSummaryHandler)
// ---------------------------------------------------------------------------

func logsSummary(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	timeRange := "1h"
	if v, ok := args["time_range"].(string); ok && v != "" {
		timeRange = v
	}
	serviceFilter, _ := args["service"].(string)
	environmentFilter, _ := args["environment"].(string)
	commitFilter, _ := args["commit_hash"].(string)

	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	// 1. Total and error counts by level.
	countParams := store.LogCountParams{
		Since:   since,
		Until:   now,
		Service: serviceFilter,
	}
	levelCounts, err := deps.LogStore.CountByLevel(ctx, countParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
	}

	totalLogs := 0
	for _, c := range levelCounts {
		totalLogs += c
	}
	errorCount := levelCounts["error"] + levelCounts["fatal"]
	var errorRatePct float64
	if totalLogs > 0 {
		errorRatePct = float64(errorCount) / float64(totalLogs) * 100
	}

	// 2. Fetch recent logs to aggregate commits and errors in Go.
	searchParams := store.LogSearchParams{
		Service:     serviceFilter,
		Environment: environmentFilter,
		CommitHash:  commitFilter,
		Start:       &since,
		End:         &now,
		Limit:       2000,
		SortAsc:     false,
	}
	entries, err := deps.LogStore.Search(ctx, searchParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
	}

	// 3. Aggregate by commit hash.
	type commitInfo struct {
		hash       string
		firstSeen  time.Time
		logCount   int
		errorCount int
	}
	commitMap := make(map[string]*commitInfo)
	for _, e := range entries {
		if e.CommitHash == "" {
			continue
		}
		ci, ok := commitMap[e.CommitHash]
		if !ok {
			ci = &commitInfo{
				hash:      e.CommitHash,
				firstSeen: e.Timestamp,
			}
			commitMap[e.CommitHash] = ci
		}
		ci.logCount++
		if e.Level == "error" || e.Level == "fatal" {
			ci.errorCount++
		}
		if e.Timestamp.Before(ci.firstSeen) {
			ci.firstSeen = e.Timestamp
		}
	}

	var activeCommits []map[string]any
	for _, ci := range commitMap {
		shortHash := ci.hash
		if len(shortHash) > 7 {
			shortHash = shortHash[:7]
		}
		activeCommits = append(activeCommits, map[string]any{
			"hash":        ci.hash,
			"short_hash":  shortHash,
			"first_seen":  ci.firstSeen.Format(time.RFC3339),
			"log_count":   ci.logCount,
			"error_count": ci.errorCount,
		})
	}
	sort.Slice(activeCommits, func(i, j int) bool {
		return activeCommits[i]["first_seen"].(string) > activeCommits[j]["first_seen"].(string)
	})
	if len(activeCommits) > 10 {
		activeCommits = activeCommits[:10]
	}

	// 4. Aggregate top errors by fingerprint (or exception_class + message).
	type errorInfo struct {
		fingerprint    string
		exceptionClass string
		message        string
		sourceFile     string
		sourceLine     int
		commitHash     string
		count          int
		firstSeen      time.Time
	}
	errorMap := make(map[string]*errorInfo)
	for _, e := range entries {
		if e.Level != "error" && e.Level != "fatal" {
			continue
		}
		// Group key: prefer fingerprint, fall back to exception_class, then message prefix.
		key := e.ErrorFingerprint
		if key == "" {
			key = e.ExceptionClass
		}
		if key == "" {
			msg := e.Message
			if len(msg) > 80 {
				msg = msg[:80]
			}
			key = msg
		}
		if key == "" {
			continue
		}

		ei, ok := errorMap[key]
		if !ok {
			ei = &errorInfo{
				fingerprint:    e.ErrorFingerprint,
				exceptionClass: e.ExceptionClass,
				message:        e.Message,
				sourceFile:     e.SourceFile,
				sourceLine:     e.SourceLine,
				commitHash:     e.CommitHash,
				firstSeen:      e.Timestamp,
			}
			errorMap[key] = ei
		}
		ei.count++
		if e.Timestamp.Before(ei.firstSeen) {
			ei.firstSeen = e.Timestamp
		}
	}

	type sortableError struct {
		key string
		ei  *errorInfo
	}
	var sortedErrors []sortableError
	for k, v := range errorMap {
		sortedErrors = append(sortedErrors, sortableError{k, v})
	}
	sort.Slice(sortedErrors, func(i, j int) bool {
		return sortedErrors[i].ei.count > sortedErrors[j].ei.count
	})

	errLimit := 10
	if len(sortedErrors) < errLimit {
		errLimit = len(sortedErrors)
	}

	var uniqueErrors []map[string]any
	for _, se := range sortedErrors[:errLimit] {
		entry := map[string]any{
			"count":      se.ei.count,
			"first_seen": se.ei.firstSeen.Format(time.RFC3339),
		}
		if se.ei.fingerprint != "" {
			entry["fingerprint"] = se.ei.fingerprint
		}
		if se.ei.exceptionClass != "" {
			entry["exception_class"] = se.ei.exceptionClass
		}
		msg := se.ei.message
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		entry["message"] = msg
		if se.ei.sourceFile != "" {
			entry["source_file"] = se.ei.sourceFile
			if se.ei.sourceLine > 0 {
				entry["source_line"] = se.ei.sourceLine
			}
		}
		if se.ei.commitHash != "" {
			entry["commit_hash"] = se.ei.commitHash
		}
		uniqueErrors = append(uniqueErrors, entry)
	}

	// 5. Slowest endpoints from request summaries.
	summaryParams := store.RequestSummarySearchParams{
		Start:  &since,
		End:    &now,
		SortBy: "duration_ms",
		Limit:  5,
	}
	summaries, _ := deps.LogStore.SearchRequestSummaries(ctx, summaryParams)

	var slowestEndpoints []map[string]any
	for _, rs := range summaries {
		ep := map[string]any{
			"path":        rs.Path,
			"duration_ms": logsRound2(rs.DurationMs),
		}
		if rs.Method != "" {
			ep["method"] = rs.Method
		}
		if rs.Controller != "" {
			ep["controller"] = rs.Controller
		}
		if rs.Action != "" {
			ep["action"] = rs.Action
		}
		if rs.SQLCount > 0 {
			ep["sql_count"] = rs.SQLCount
		}
		if rs.NPlusOne {
			ep["n_plus_one"] = true
		}
		slowestEndpoints = append(slowestEndpoints, ep)
	}

	// Build response.
	resp := map[string]any{
		"time_range":     map[string]any{"start": since.Format(time.RFC3339), "end": now.Format(time.RFC3339), "window": timeRange},
		"total_logs":     totalLogs,
		"error_count":    errorCount,
		"error_rate_pct": logsRound2(errorRatePct),
		"by_level":       levelCounts,
	}
	if len(activeCommits) > 0 {
		resp["active_commits"] = activeCommits
	}
	if len(uniqueErrors) > 0 {
		resp["unique_errors"] = uniqueErrors
	}
	if len(slowestEndpoints) > 0 {
		resp["slowest_endpoints"] = slowestEndpoints
	}

	// 6. Unresolved error groups (from ErrorGroupStore).
	if deps.ErrorGroupStore != nil {
		unresolvedCount, _ := deps.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved)
		if unresolvedCount > 0 {
			resp["unresolved_error_groups"] = unresolvedCount
			topErrors, _ := deps.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
				Status: store.ErrorGroupUnresolved,
				SortBy: "occurrence_count",
				Limit:  3,
			})
			if len(topErrors) > 0 {
				var top []map[string]any
				for _, eg := range topErrors {
					entry := map[string]any{
						"fingerprint":      eg.Fingerprint,
						"occurrence_count": eg.OccurrenceCount,
						"last_seen_at":     eg.LastSeenAt.Format(time.RFC3339),
						"status":           string(eg.Status),
					}
					if eg.ExceptionClass != "" {
						entry["exception_class"] = eg.ExceptionClass
					}
					msg := eg.Message
					if len(msg) > 120 {
						msg = msg[:120] + "..."
					}
					entry["message"] = msg
					if eg.Service != "" {
						entry["service"] = eg.Service
					}
					top = append(top, entry)
				}
				resp["top_error_groups"] = top
			}
		}
	}

	// Warnings to draw attention.
	var warnings []string
	if errorRatePct > 5 {
		warnings = append(warnings, fmt.Sprintf("Error rate is %.1f%% — investigate the top unique errors below.", errorRatePct))
	}
	if len(activeCommits) > 1 {
		warnings = append(warnings, fmt.Sprintf("%d different commits are active — check if a recent deploy introduced errors.", len(activeCommits)))
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	// Suggested next tools based on findings.
	var suggestions []ToolSuggestion
	if errorRatePct > 5 {
		sugArgs := map[string]any{"status": "unresolved"}
		if serviceFilter != "" {
			sugArgs["service"] = serviceFilter
		}
		suggestions = append(suggestions, Suggest("error_groups", "High error rate — check unresolved errors", sugArgs))
	}
	if len(uniqueErrors) > 0 {
		if fp, ok := uniqueErrors[0]["fingerprint"].(string); ok && fp != "" {
			suggestions = append(suggestions, Suggest("error_detail", "Investigate the most frequent error", map[string]any{"fingerprint": fp}))
		}
	}
	if len(slowestEndpoints) > 0 {
		suggestions = append(suggestions, Suggest("request_performance", "Investigate slow endpoints", map[string]any{"sort_by": "duration_ms"}))
	}
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// action: performance — request performance analysis (from requestPerformanceHandler)
// ---------------------------------------------------------------------------

func logsPerformance(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	// Parse time range (default: 24h).
	timeRange := "24h"
	if v, ok := args["time_range"].(string); ok && v != "" {
		timeRange = v
	}
	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v. Use formats like '1h', '24h', '7d'.", err)), nil
	}
	now := time.Now().UTC()
	start := now.Add(-duration)

	params := store.RequestSummarySearchParams{
		Start: &start,
		End:   &now,
	}

	if v, ok := args["controller"].(string); ok && v != "" {
		params.Controller = v
	}
	if v, ok := args["path"].(string); ok && v != "" {
		params.Path = v
	}
	if v, ok := args["n_plus_one_only"].(bool); ok {
		params.NPlusOneOnly = v
	}
	if v, ok := args["min_duration_ms"].(float64); ok && v > 0 {
		params.MinDurationMs = v
	}
	if v, ok := args["min_sql_count"].(float64); ok && v > 0 {
		params.MinSQLCount = int(v)
	}

	sortBy := "duration_ms"
	if v, ok := args["sort_by"].(string); ok && v != "" {
		switch v {
		case "duration_ms", "sql_count", "db_time_ms", "duplicate_queries":
			sortBy = v
		default:
			return mcp.NewToolResultError(fmt.Sprintf("invalid sort_by: %q. Use duration_ms, sql_count, db_time_ms, or duplicate_queries.", v)), nil
		}
	}
	params.SortBy = sortBy

	limit := 20
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
		if limit > 100 {
			limit = 100
		}
	}
	params.Limit = limit

	results, err := deps.LogStore.SearchRequestSummaries(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to search request summaries: %v", err)), nil
	}

	if len(results) == 0 {
		hint := "No request performance data found for the given filters."
		if params.NPlusOneOnly {
			hint += " No N+1 queries detected in this time range — that's good!"
		}
		hint += " Try extending the time_range or broadening your filters."
		return mcp.NewToolResultText(hint), nil
	}

	// Build response entries.
	perfEntries := make([]map[string]any, 0, len(results))
	var totalDuration, totalSQLCount float64
	nPlusOneCount := 0
	for _, r := range results {
		entry := map[string]any{
			"log_id":      r.LogID,
			"timestamp":   r.Timestamp.Format(time.RFC3339),
			"duration_ms": r.DurationMs,
			"sql_count":   r.SQLCount,
			"db_time_ms":  r.DBTimeMs,
			"n_plus_one":  r.NPlusOne,
		}
		if r.Controller != "" {
			entry["controller"] = r.Controller
		}
		if r.Action != "" {
			entry["action"] = r.Action
		}
		if r.Method != "" {
			entry["method"] = r.Method
		}
		if r.Path != "" {
			entry["path"] = r.Path
		}
		if r.Status > 0 {
			entry["status"] = r.Status
		}
		if r.SQLTotalMs > 0 {
			entry["sql_total_ms"] = r.SQLTotalMs
		}
		if r.DuplicateQueries > 0 {
			entry["duplicate_queries"] = r.DuplicateQueries
			entry["worst_duplicate_count"] = r.WorstDuplicateCount
		}
		if r.TopDuplicates != "" {
			entry["top_duplicates"] = r.TopDuplicates
		}
		if r.Service != "" {
			entry["service"] = r.Service
		}
		if r.TraceID != "" {
			entry["trace_id"] = r.TraceID
		}

		perfEntries = append(perfEntries, entry)
		totalDuration += r.DurationMs
		totalSQLCount += float64(r.SQLCount)
		if r.NPlusOne {
			nPlusOneCount++
		}
	}

	// Summary stats.
	summary := map[string]any{
		"total_requests":   len(results),
		"avg_duration":     totalDuration / float64(len(results)),
		"n_plus_one_count": nPlusOneCount,
		"avg_sql_count":    totalSQLCount / float64(len(results)),
	}

	// Hints.
	var hints []string
	if nPlusOneCount > 0 {
		hints = append(hints, fmt.Sprintf("%d request(s) have N+1 queries — use log_search with the trace_id to see full SQL details", nPlusOneCount))
	}
	avgSQL := totalSQLCount / float64(len(results))
	if avgSQL > 20 {
		hints = append(hints, fmt.Sprintf("Average SQL count is %.0f — consider caching or query optimization", avgSQL))
	}
	avgDur := totalDuration / float64(len(results))
	if avgDur > 1000 {
		hints = append(hints, fmt.Sprintf("Average request duration is %.0fms — investigate slowest endpoints", avgDur))
	}
	if len(hints) == 0 {
		hints = append(hints, "Request performance looks healthy for the analyzed period")
	}

	resp := map[string]any{
		"entries": perfEntries,
		"summary": summary,
		"hints":   hints,
	}

	// Suggested next tools based on findings.
	var suggestions []ToolSuggestion
	if nPlusOneCount > 0 {
		suggestions = append(suggestions, Suggest("log_search", "N+1 queries detected — search for SQL details", map[string]any{"level": "debug"}))
	}
	if avgSQL > 20 {
		suggestions = append(suggestions, Suggest("query_stats", "High SQL count — check query performance", nil))
	}
	if len(perfEntries) > 0 {
		suggestions = append(suggestions, Suggest("diagnose", "Get full system overview", nil))
	}
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// action: trace — distributed trace assembly (from traceLookupHandler)
// ---------------------------------------------------------------------------

func logsTrace(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	traceID, _ := args["trace_id"].(string)
	if traceID == "" {
		return mcp.NewToolResultError("trace_id is required"), nil
	}

	includeContext := false
	if v, ok := args["include_context"].(bool); ok {
		includeContext = v
	}

	// Fetch all log entries for this trace.
	entries, err := deps.LogStore.Search(ctx, store.LogSearchParams{
		TraceID: traceID,
		Limit:   1000,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
	}

	// Sort by timestamp ascending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	if len(entries) == 0 {
		resp := map[string]any{
			"trace_id":      traceID,
			"total_entries": 0,
			"message":       "No log entries found for this trace ID",
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}

	firstTime := entries[0].Timestamp
	var lastTime time.Time
	if len(entries) > 0 {
		lastTime = entries[len(entries)-1].Timestamp
	}

	// Build timeline.
	var timeline []map[string]any
	servicesSet := map[string]bool{}
	hasErrors := false

	for _, e := range entries {
		elapsed := e.Timestamp.Sub(firstTime).Milliseconds()
		entry := map[string]any{
			"timestamp":  e.Timestamp.Format(time.RFC3339Nano),
			"service":    e.Service,
			"level":      e.Level,
			"message":    e.Message,
			"elapsed_ms": elapsed,
		}
		timeline = append(timeline, entry)
		if e.Service != "" {
			servicesSet[e.Service] = true
		}
		if e.Level == "error" || e.Level == "fatal" {
			hasErrors = true
		}
	}

	// Build service summary.
	type svcStats struct {
		entries   int
		errors    int
		firstTime time.Time
		lastTime  time.Time
	}
	svcMap := map[string]*svcStats{}
	for _, e := range entries {
		svc := e.Service
		if svc == "" {
			svc = "(unknown)"
		}
		s, ok := svcMap[svc]
		if !ok {
			s = &svcStats{firstTime: e.Timestamp, lastTime: e.Timestamp}
			svcMap[svc] = s
		}
		s.entries++
		if e.Timestamp.Before(s.firstTime) {
			s.firstTime = e.Timestamp
		}
		if e.Timestamp.After(s.lastTime) {
			s.lastTime = e.Timestamp
		}
		if e.Level == "error" || e.Level == "fatal" {
			s.errors++
		}
	}

	var serviceSummary []map[string]any
	var servicesList []string
	for svc := range servicesSet {
		servicesList = append(servicesList, svc)
	}
	sort.Strings(servicesList)

	for _, svc := range servicesList {
		s := svcMap[svc]
		timeSpent := s.lastTime.Sub(s.firstTime).Milliseconds()
		serviceSummary = append(serviceSummary, map[string]any{
			"service":       svc,
			"entries":       s.entries,
			"errors":        s.errors,
			"time_spent_ms": timeSpent,
		})
	}

	// Detect warnings.
	var warnings []string

	// Find first error as root cause.
	for _, e := range entries {
		if e.Level == "error" || e.Level == "fatal" {
			elapsed := e.Timestamp.Sub(firstTime).Milliseconds()
			warnings = append(warnings, fmt.Sprintf("Error in %s at +%dms — this is likely the root cause", e.Service, elapsed))
			break
		}
	}

	// Gap detection: flag gaps >500ms between consecutive entries.
	for i := 1; i < len(entries); i++ {
		gap := entries[i].Timestamp.Sub(entries[i-1].Timestamp)
		if gap > 500*time.Millisecond {
			prevSvc := entries[i-1].Service
			currSvc := entries[i].Service
			if prevSvc != currSvc {
				warnings = append(warnings, fmt.Sprintf("Gap of %dms between %s→%s — possible network or queue delay", gap.Milliseconds(), prevSvc, currSvc))
			}
		}
	}

	totalDuration := lastTime.Sub(firstTime).Milliseconds()

	resp := map[string]any{
		"trace_id":          traceID,
		"total_entries":     len(entries),
		"total_duration_ms": totalDuration,
		"services_touched":  servicesList,
		"has_errors":        hasErrors,
		"timeline":          timeline,
		"service_summary":   serviceSummary,
	}

	// Context entries (surrounding logs from each service).
	if includeContext && len(entries) > 0 {
		var contextEntries []map[string]any
		for svc, stats := range svcMap {
			ctxStart := stats.firstTime.Add(-2 * time.Second)
			ctxEnd := stats.lastTime.Add(2 * time.Second)
			ctxLogs, err := deps.LogStore.Search(ctx, store.LogSearchParams{
				Service: svc,
				Start:   &ctxStart,
				End:     &ctxEnd,
				Limit:   50,
			})
			if err != nil {
				continue
			}
			for _, cl := range ctxLogs {
				if cl.TraceID == traceID {
					continue // skip entries already in the trace
				}
				contextEntries = append(contextEntries, map[string]any{
					"timestamp": cl.Timestamp.Format(time.RFC3339Nano),
					"service":   cl.Service,
					"level":     cl.Level,
					"message":   cl.Message,
				})
			}
		}
		resp["context_entries"] = contextEntries
	}

	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	// Record trace ID in session if callback is provided.
	if deps.TraceSessionRecorder != nil {
		deps.TraceSessionRecorder(traceID)
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// action: compare — compare metrics between two time periods (from comparePeriodsHandler)
// ---------------------------------------------------------------------------

func logsCompare(ctx context.Context, args map[string]any, deps LogsDeps) (*mcp.CallToolResult, error) {
	metric, _ := args["metric"].(string)
	if metric == "" {
		return mcp.NewToolResultError("metric is required (errors, log_volume)"), nil
	}

	currentPeriod := "last_1h"
	if v, ok := args["current_period"].(string); ok && v != "" {
		currentPeriod = v
	}

	baselinePeriod := "previous"
	if v, ok := args["baseline_period"].(string); ok && v != "" {
		baselinePeriod = v
	}

	serviceFilter, _ := args["service"].(string)

	now := time.Now().UTC()
	currentStart, currentEnd, err := logsResolvePeriod(currentPeriod, now)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid current_period: %v", err)), nil
	}

	baseStart, baseEnd, err := logsResolveBaseline(baselinePeriod, currentStart, currentEnd)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid baseline_period: %v", err)), nil
	}

	switch metric {
	case "errors":
		return logsCompareErrors(ctx, deps.LogStore, currentStart, currentEnd, baseStart, baseEnd, serviceFilter)
	case "log_volume":
		return logsCompareLogVolume(ctx, deps.LogStore, currentStart, currentEnd, baseStart, baseEnd, serviceFilter)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("invalid metric: %q (use errors or log_volume)", metric)), nil
	}
}

func logsCompareErrors(ctx context.Context, ls store.LogStore, curStart, curEnd, baseStart, baseEnd time.Time, service string) (*mcp.CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service}

	curSvc, err := ls.CountByService(ctx, curParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count current errors: %v", err)), nil
	}
	baseSvc, err := ls.CountByService(ctx, baseParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count baseline errors: %v", err)), nil
	}

	curTotal := 0
	curByService := map[string]int{}
	for _, s := range curSvc {
		curTotal += s.ErrorCount
		if s.ErrorCount > 0 {
			curByService[s.Service] = s.ErrorCount
		}
	}

	baseTotal := 0
	baseByService := map[string]int{}
	for _, s := range baseSvc {
		baseTotal += s.ErrorCount
		if s.ErrorCount > 0 {
			baseByService[s.Service] = s.ErrorCount
		}
	}

	changePct, direction := logsCalcChange(baseTotal, curTotal)

	var movers []map[string]any
	allServices := map[string]bool{}
	for svc := range curByService {
		allServices[svc] = true
	}
	for svc := range baseByService {
		allServices[svc] = true
	}
	for svc := range allServices {
		from := baseByService[svc]
		to := curByService[svc]
		pct, _ := logsCalcChange(from, to)
		movers = append(movers, map[string]any{
			"service":    svc,
			"from":       from,
			"to":         to,
			"change_pct": pct,
		})
	}
	sort.Slice(movers, func(i, j int) bool {
		return math.Abs(movers[i]["change_pct"].(float64)) > math.Abs(movers[j]["change_pct"].(float64))
	})
	if len(movers) > 5 {
		movers = movers[:5]
	}

	var warnings []string
	if direction == "increase" && math.Abs(changePct) > 50 {
		warnings = append(warnings, fmt.Sprintf("Error rate increased %.0f%% compared to the baseline period", changePct))
	}
	if len(movers) > 0 {
		topMover := movers[0]
		topPct := math.Abs(topMover["change_pct"].(float64))
		if topPct > 100 {
			warnings = append(warnings, fmt.Sprintf("Service '%s' errors changed %.0f%% — largest contributor", topMover["service"], topMover["change_pct"]))
		}
	}

	resp := map[string]any{
		"metric": "errors",
		"current": map[string]any{
			"period":       logsPeriodInfo(curStart, curEnd),
			"total_errors": curTotal,
			"by_service":   curByService,
		},
		"baseline": map[string]any{
			"period":       logsPeriodInfo(baseStart, baseEnd),
			"total_errors": baseTotal,
			"by_service":   baseByService,
		},
		"changes": map[string]any{
			"total_change_pct": changePct,
			"direction":        direction,
			"biggest_movers":   movers,
		},
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func logsCompareLogVolume(ctx context.Context, ls store.LogStore, curStart, curEnd, baseStart, baseEnd time.Time, service string) (*mcp.CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service}

	curLevels, err := ls.CountByLevel(ctx, curParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count current logs: %v", err)), nil
	}
	baseLevels, err := ls.CountByLevel(ctx, baseParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count baseline logs: %v", err)), nil
	}

	curTotal := 0
	for _, c := range curLevels {
		curTotal += c
	}
	baseTotal := 0
	for _, c := range baseLevels {
		baseTotal += c
	}

	changePct, direction := logsCalcChange(baseTotal, curTotal)

	var levelChanges []map[string]any
	allLevels := map[string]bool{}
	for l := range curLevels {
		allLevels[l] = true
	}
	for l := range baseLevels {
		allLevels[l] = true
	}
	for l := range allLevels {
		from := baseLevels[l]
		to := curLevels[l]
		pct, _ := logsCalcChange(from, to)
		levelChanges = append(levelChanges, map[string]any{
			"level":      l,
			"from":       from,
			"to":         to,
			"change_pct": pct,
		})
	}

	resp := map[string]any{
		"metric": "log_volume",
		"current": map[string]any{
			"period":   logsPeriodInfo(curStart, curEnd),
			"total":    curTotal,
			"by_level": curLevels,
		},
		"baseline": map[string]any{
			"period":   logsPeriodInfo(baseStart, baseEnd),
			"total":    baseTotal,
			"by_level": baseLevels,
		},
		"changes": map[string]any{
			"total_change_pct": changePct,
			"direction":        direction,
			"by_level":         levelChanges,
		},
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Shared helpers (used by both logs.go and errors.go)
// ---------------------------------------------------------------------------

// parseTimeRange parses shorthand like "15m", "1h", "6h", "24h", "7d".
func parseTimeRange(s string) (time.Duration, error) {
	if s == "" {
		return time.Hour, nil
	}

	// Try standard Go duration first.
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}

	// Handle "d" suffix.
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(numStr, "%d", &days); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}

	return 0, fmt.Errorf("cannot parse %q as duration (use 15m, 1h, 6h, 24h, 7d)", s)
}

// ---------------------------------------------------------------------------
// Helpers (prefixed with "logs" to avoid collisions with errors.go helpers)
// ---------------------------------------------------------------------------

// logsBuildTimeHistogram creates a compact time distribution of log entries.
// Auto-selects bucket size based on the time span of the results.
func logsBuildTimeHistogram(entries []store.LogEntry) map[string]any {
	if len(entries) == 0 {
		return nil
	}

	// Find time range.
	earliest := entries[0].Timestamp
	latest := entries[0].Timestamp
	for _, e := range entries[1:] {
		if e.Timestamp.Before(earliest) {
			earliest = e.Timestamp
		}
		if e.Timestamp.After(latest) {
			latest = e.Timestamp
		}
	}

	span := latest.Sub(earliest)
	if span <= 0 {
		return nil
	}

	// Auto-select bucket size.
	var bucketSize time.Duration
	var bucketLabel string
	switch {
	case span <= 5*time.Minute:
		bucketSize = 30 * time.Second
		bucketLabel = "30s"
	case span <= 30*time.Minute:
		bucketSize = time.Minute
		bucketLabel = "1m"
	case span <= 2*time.Hour:
		bucketSize = 5 * time.Minute
		bucketLabel = "5m"
	case span <= 12*time.Hour:
		bucketSize = 30 * time.Minute
		bucketLabel = "30m"
	default:
		bucketSize = time.Hour
		bucketLabel = "1h"
	}

	// Build buckets.
	buckets := make(map[string]int)
	for _, e := range entries {
		bucketStart := e.Timestamp.Truncate(bucketSize)
		key := bucketStart.Format(time.RFC3339)
		buckets[key]++
	}

	return map[string]any{
		"bucket_size": bucketLabel,
		"buckets":     buckets,
	}
}

var (
	logsReUUID      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	logsReIP        = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)
	logsReNumber    = regexp.MustCompile(`\b\d+\b`)
	logsReQuoted    = regexp.MustCompile(`"[^"]*"`)
	logsReTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}[^\s]*`)
)

// logsNormalizeMessage strips variable parts (UUIDs, IPs, numbers, timestamps, quoted strings)
// to produce a pattern key for clustering similar error messages.
func logsNormalizeMessage(msg string) string {
	msg = logsReTimestamp.ReplaceAllString(msg, "*")
	msg = logsReUUID.ReplaceAllString(msg, "*")
	msg = logsReIP.ReplaceAllString(msg, "*")
	msg = logsReQuoted.ReplaceAllString(msg, `"*"`)
	msg = logsReNumber.ReplaceAllString(msg, "*")
	return msg
}

func logsToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

func logsRound2(f float64) float64 {
	return float64(int(f*100)) / 100
}

func logsResolvePeriod(period string, now time.Time) (time.Time, time.Time, error) {
	switch period {
	case "last_1h":
		return now.Add(-time.Hour), now, nil
	case "last_6h":
		return now.Add(-6 * time.Hour), now, nil
	case "last_24h":
		return now.Add(-24 * time.Hour), now, nil
	case "today":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return start, now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown period %q (use last_1h, last_6h, last_24h, today)", period)
	}
}

func logsResolveBaseline(baseline string, curStart, curEnd time.Time) (time.Time, time.Time, error) {
	duration := curEnd.Sub(curStart)
	switch baseline {
	case "previous":
		return curStart.Add(-duration), curStart, nil
	case "yesterday_same_time":
		return curStart.Add(-24 * time.Hour), curEnd.Add(-24 * time.Hour), nil
	case "last_week_same_time":
		return curStart.Add(-168 * time.Hour), curEnd.Add(-168 * time.Hour), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown baseline %q (use previous, yesterday_same_time, last_week_same_time)", baseline)
	}
}

func logsCalcChange(baseline, current int) (float64, string) {
	if baseline == 0 {
		if current == 0 {
			return 0, "unchanged"
		}
		return 100, "new"
	}
	pct := float64(current-baseline) / float64(baseline) * 100
	pct = logsRound2(pct)
	if pct > 0 {
		return pct, "increase"
	} else if pct < 0 {
		return pct, "decrease"
	}
	return 0, "unchanged"
}

func logsPeriodInfo(start, end time.Time) map[string]string {
	return map[string]string{
		"start": start.Format(time.RFC3339),
		"end":   end.Format(time.RFC3339),
	}
}
