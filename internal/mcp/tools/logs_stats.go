package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/domain/logs"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: stats — aggregate log statistics (from logStatsHandler)
// ---------------------------------------------------------------------------

func LogsStats(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	InitLogsDeps(&deps)
	timeRange := ArgStringDefault(args, "time_range", "1h")
	groupBy := ArgStringDefault(args, "group_by", "level")
	serviceFilter := ArgString(args, "service")
	levelFilter := ArgString(args, "level")
	bucketInterval := ArgStringDefault(args, "bucket_interval", "5m")

	duration, err := ParseTimeRange(timeRange)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
	}

	bucketDur, err := ParseTimeRange(bucketInterval)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid bucket_interval: %v", err)), nil
	}

	// Resolve the env scope so these aggregates only span environments the
	// caller's token is authorized for (an empty Environment matches ALL envs).
	environment, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	params := store.LogCountParams{
		Since:       since,
		Until:       now,
		Service:     serviceFilter,
		Level:       levelFilter,
		Environment: environment,
	}

	switch groupBy {
	case "level":
		return LogsStatsByLevel(ctx, deps.Logs, params, since, now, bucketDur)
	case "service":
		return LogsStatsByService(ctx, deps.Logs, params, since, now)
	case "pattern":
		return LogsStatsByPattern(ctx, deps.Logs, since, now, serviceFilter, environment)
	default:
		return NewToolResultError(fmt.Sprintf("invalid group_by: %q (use level, service, or pattern)", groupBy)), nil
	}
}

func LogsStatsByLevel(ctx context.Context, svc *logs.Service, params store.LogCountParams, since, until time.Time, bucketDur time.Duration) (*CallToolResult, error) {
	lc, err := svc.CountByLevel(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
	}

	// Build trend buckets.
	var trend []map[string]any
	for t := since; t.Before(until); t = t.Add(bucketDur) {
		bucketEnd := t.Add(bucketDur)
		if bucketEnd.After(until) {
			bucketEnd = until
		}
		bucketParams := store.LogCountParams{
			Since:       t,
			Until:       bucketEnd,
			Service:     params.Service,
			Level:       params.Level,
			Environment: params.Environment,
		}
		bucketLC, err := svc.CountByLevel(ctx, bucketParams)
		if err != nil {
			continue
		}
		bucket := map[string]any{
			"bucket": t.Format(time.RFC3339),
		}
		for level, count := range bucketLC.ByLevel {
			bucket[level] = count
		}
		trend = append(trend, bucket)
	}

	var warnings []string
	// Detect error rate spike in last bucket vs average.
	if len(trend) >= 2 {
		lastBucket := trend[len(trend)-1]
		lastErrors := toInt(lastBucket["error"]) + toInt(lastBucket["fatal"])
		avgErrors := float64(lc.ErrorCount) / float64(len(trend))
		if avgErrors > 0 && float64(lastErrors) > avgErrors*1.4 {
			pctIncrease := (float64(lastErrors) - avgErrors) / avgErrors * 100
			warnings = append(warnings, fmt.Sprintf("Error rate increased %.0f%% in the last bucket compared to the average", pctIncrease))
		}
	}

	resp := map[string]any{
		"time_range":     map[string]any{"start": since.Format(time.RFC3339), "end": until.Format(time.RFC3339)},
		"total_logs":     lc.Total,
		"by_level":       lc.ByLevel,
		"error_rate_pct": round2(lc.ErrorRate),
	}
	if len(trend) > 0 {
		resp["trend"] = trend
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	return JSONResult(resp)
}

func LogsStatsByService(ctx context.Context, svc *logs.Service, params store.LogCountParams, since, until time.Time) (*CallToolResult, error) {
	services, err := svc.CountByService(ctx, params)
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
			"error_rate_pct": round2(errRate),
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

	return JSONResult(resp)
}

func LogsStatsByPattern(ctx context.Context, svc *logs.Service, since, until time.Time, service, environment string) (*CallToolResult, error) {
	// Fetch error/fatal logs for pattern clustering.
	errorResult, err := svc.Search(ctx, store.LogSearchParams{
		Level:       "error",
		Service:     service,
		Environment: environment,
		Start:       &since,
		End:         &until,
		Limit:       10000,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
	}
	errorLogs := errorResult.Entries

	// Also fetch fatal logs.
	fatalResult, err := svc.Search(ctx, store.LogSearchParams{
		Level:       "fatal",
		Service:     service,
		Environment: environment,
		Start:       &since,
		End:         &until,
		Limit:       10000,
	})
	if err == nil {
		errorLogs = append(errorLogs, fatalResult.Entries...)
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
			"pct_of_errors":  round2(pctOfErrors),
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

	return JSONResult(resp)
}
