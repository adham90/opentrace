package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"


	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: summary — debugging overview (from logSummaryHandler)
// ---------------------------------------------------------------------------

func LogsSummary(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	timeRange := "1h"
	if v, ok := args["time_range"].(string); ok && v != "" {
		timeRange = v
	}
	serviceFilter, _ := args["service"].(string)
	environmentFilter, _ := args["environment"].(string)
	commitFilter, _ := args["commit_hash"].(string)

	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
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
		return NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
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
		return NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
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
		sugArgs["action"] = "list"
		suggestions = append(suggestions, Suggest("errors", "High error rate — check unresolved errors", sugArgs))
	}
	if len(uniqueErrors) > 0 {
		if fp, ok := uniqueErrors[0]["fingerprint"].(string); ok && fp != "" {
			suggestions = append(suggestions, Suggest("errors", "Investigate the most frequent error", map[string]any{"action": "detail", "fingerprint": fp}))
		}
	}
	if len(slowestEndpoints) > 0 {
		suggestions = append(suggestions, Suggest("logs", "Investigate slow endpoints", map[string]any{"action": "performance", "sort_by": "duration_ms"}))
	}
	withSuggestionsRanked(resp, deps.Ranker, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}
