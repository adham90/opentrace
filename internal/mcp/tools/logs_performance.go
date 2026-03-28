package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/pkg/store"
)

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
		suggestions = append(suggestions, Suggest("logs", "N+1 queries detected — search for SQL details", map[string]any{"action": "search", "level": "debug"}))
	}
	if avgSQL > 20 {
		suggestions = append(suggestions, Suggest("database", "High SQL count — check query performance", map[string]any{"action": "queries"}))
	}
	if len(perfEntries) > 0 {
		suggestions = append(suggestions, Suggest("overview", "Get full system overview", map[string]any{"action": "diagnose"}))
	}
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
