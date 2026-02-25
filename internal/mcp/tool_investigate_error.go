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

// investigateErrorHandler returns a handler that deep-dives into a single error
// log entry, returning ALL available context in one call: exception details,
// backtrace, request params, SQL queries, surrounding logs, and related error
// group. This eliminates the need for 5+ sequential tool calls when debugging.
func investigateErrorHandler(ls store.LogStore, egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		var anchor *store.LogEntry
		var err error

		// Accept either log_id or trace_id as the entry point.
		if logID, ok := args["log_id"].(float64); ok && logID > 0 {
			anchor, err = ls.GetByID(ctx, int64(logID))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("log entry %d not found: %v", int64(logID), err)), nil
			}
		} else if traceID, ok := args["trace_id"].(string); ok && traceID != "" {
			// Find the primary error entry in this trace.
			traceEntries, searchErr := ls.Search(ctx, store.LogSearchParams{
				TraceID: traceID,
				Limit:   50,
				SortAsc: true,
			})
			if searchErr != nil || len(traceEntries) == 0 {
				return mcp.NewToolResultError(fmt.Sprintf("no log entries found for trace_id=%s", traceID)), nil
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
			return mcp.NewToolResultError("Either log_id (positive integer) or trace_id (string) is required"), nil
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
			traceEntries, _ := ls.Search(ctx, store.LogSearchParams{
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
		beforeEntries, _ := ls.Search(ctx, store.LogSearchParams{
			End:     &anchor.Timestamp,
			Service: anchor.Service,
			Limit:   5,
			SortAsc: false,
		})
		afterStart := anchor.Timestamp.Add(time.Millisecond) // avoid self
		afterEntries, _ := ls.Search(ctx, store.LogSearchParams{
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
		if anchor.ErrorFingerprint != "" && egs != nil {
			if eg, err := egs.Get(ctx, anchor.ErrorFingerprint); err == nil {
				resp["error_group"] = map[string]any{
					"fingerprint":      eg.Fingerprint,
					"status":           string(eg.Status),
					"occurrence_count": eg.OccurrenceCount,
					"first_seen_at":    eg.FirstSeenAt.Format(time.RFC3339),
					"last_seen_at":     eg.LastSeenAt.Format(time.RFC3339),
					"reopened_count":   eg.ReopenedCount,
				}
			}
		}

		// Suggested next steps.
		var suggestions []ToolSuggestion
		if anchor.ErrorFingerprint != "" {
			suggestions = append(suggestions, suggest("error_detail", "Full error group history and lifecycle", map[string]any{
				"fingerprint": anchor.ErrorFingerprint,
			}))
			suggestions = append(suggestions, suggest("log_search", "Find all occurrences of this error", map[string]any{
				"error_fingerprint": anchor.ErrorFingerprint,
			}))
		}
		if anchor.ExceptionClass != "" {
			suggestions = append(suggestions, suggest("log_search", "Find all logs with this exception class", map[string]any{
				"exception_class": anchor.ExceptionClass,
			}))
		}
		if anchor.ErrorFingerprint != "" {
			suggestions = append(suggestions, suggest("resolve_error", "Mark as resolved after fixing", map[string]any{
				"fingerprint": anchor.ErrorFingerprint,
			}))
		}
		withSuggestions(resp, suggestions...)

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
