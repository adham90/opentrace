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
// action: stats — aggregate log statistics (from logStatsHandler)
// ---------------------------------------------------------------------------

func LogsStats(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
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
		return NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
	}

	bucketDur, err := parseTimeRange(bucketInterval)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid bucket_interval: %v", err)), nil
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
		return LogsStatsByLevel(ctx, deps.LogStore, params, since, now, bucketDur)
	case "service":
		return LogsStatsByService(ctx, deps.LogStore, params, since, now)
	case "pattern":
		return LogsStatsByPattern(ctx, deps.LogStore, since, now, serviceFilter)
	default:
		return NewToolResultError(fmt.Sprintf("invalid group_by: %q (use level, service, or pattern)", groupBy)), nil
	}
}

func LogsStatsByLevel(ctx context.Context, ls store.LogStore, params store.LogCountParams, since, until time.Time, bucketDur time.Duration) (*CallToolResult, error) {
	counts, err := ls.CountByLevel(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
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
	return NewToolResultText(string(data)), nil
}

func LogsStatsByService(ctx context.Context, ls store.LogStore, params store.LogCountParams, since, until time.Time) (*CallToolResult, error) {
	services, err := ls.CountByService(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
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
	return NewToolResultText(string(data)), nil
}

func LogsStatsByPattern(ctx context.Context, ls store.LogStore, since, until time.Time, service string) (*CallToolResult, error) {
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
		return NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
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
	return NewToolResultText(string(data)), nil
}
