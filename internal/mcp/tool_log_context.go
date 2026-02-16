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

// logContextHandler returns a handler that fetches surrounding log entries
// around a given log ID. This is the "zoom in" tool — after log_search finds
// something interesting, use this to see what happened before and after.
func logContextHandler(ls store.LogStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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
		anchor, err := ls.GetByID(ctx, int64(logID))
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
		beforeEntries, err := ls.Search(ctx, beforeParams)
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
		afterEntries, err := ls.Search(ctx, afterParams)
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

		entries := make([]contextEntry, 0, len(beforeEntries)+1+len(afterEntries))
		for _, e := range beforeEntries {
			entries = append(entries, toContextEntry(e, "before", true))
		}
		entries = append(entries, toContextEntry(*anchor, "anchor", false))
		for _, e := range afterEntries {
			entries = append(entries, toContextEntry(e, "after", true))
		}

		resp := map[string]any{
			"anchor_id":    int64(logID),
			"same_service": sameService,
			"before_count": len(beforeEntries),
			"after_count":  len(afterEntries),
			"entries":      entries,
		}

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// truncate returns the first n characters of s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
