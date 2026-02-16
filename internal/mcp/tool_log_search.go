package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// logSearchHandler returns a handler that searches log entries with full-text
// search and filters. Returns individual log entries (unlike log_stats which
// returns aggregated counts).
func logSearchHandler(ls store.LogStore, egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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

		entries, err := ls.Search(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to search logs: %v. Verify your query syntax and filters.", err)), nil
		}

		// If FTS query returned nothing, try a fallback LIKE search against
		// the service field (expanded search scope).
		if len(entries) == 0 && query != "" && service == "" {
			// Try matching as a service name.
			fallbackParams := params
			fallbackParams.Query = ""
			fallbackParams.Service = query
			entries, _ = ls.Search(ctx, fallbackParams)
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
		if egs != nil {
			seen := make(map[string]bool)
			for _, e := range entries {
				if e.ErrorFingerprint != "" && !seen[e.ErrorFingerprint] {
					seen[e.ErrorFingerprint] = true
					if eg, err := egs.Get(ctx, e.ErrorFingerprint); err == nil {
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

		resp := map[string]any{
			"total_returned": len(results),
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

		// Time histogram (improvement G): bucket results by time to show distribution.
		if len(entries) > 1 {
			resp["time_distribution"] = buildTimeHistogram(entries)
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// buildTimeHistogram creates a compact time distribution of log entries.
// Auto-selects bucket size based on the time span of the results.
func buildTimeHistogram(entries []store.LogEntry) map[string]any {
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
