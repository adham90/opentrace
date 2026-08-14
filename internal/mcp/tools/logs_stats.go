package tools

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/domain/logs"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: stats — aggregate log statistics (from logStatsHandler)
// ---------------------------------------------------------------------------

const (
	// maxTrendBuckets caps the trend loop. Every bucket is a separate scan of
	// the store, so an unbounded bucket count turns one tool call into tens of
	// thousands of sequential queries (30d / 30s = 86,400). Refuse instead of
	// grinding: the caller can widen bucket_interval and ask again.
	maxTrendBuckets = 500

	// maxServiceBreakdown bounds the per-service follow-up count queries issued
	// by group_by=service. Services are visited highest-volume first.
	maxServiceBreakdown = 50

	// maxPatternSample is the largest sample the log service will return for one
	// search (domain.ClampLimit(_, 50, 500)). Requesting more silently yields
	// this many rows, so ask for exactly it and report the sample honestly.
	maxPatternSample = 500
)

func LogsStats(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	InitLogsDeps(&deps)
	groupBy := ArgStringDefault(args, "group_by", "level")
	serviceFilter := ArgString(args, "service")
	levelFilter := ArgString(args, "level")
	bucketInterval := ArgStringDefault(args, "bucket_interval", "5m")

	since, _, err := ResolveWindow(args, "1h")
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	bucketDur, err := ParseTimeRange(bucketInterval)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid bucket_interval: %v", err)), nil
	}
	// ParseTimeRange delegates to time.ParseDuration, which happily accepts
	// "0s" and "-5m". Both make the trend loop below non-terminating (t never
	// advances, or walks backwards forever) while appending a bucket per pass.
	if bucketDur <= 0 {
		return NewToolResultError(fmt.Sprintf(
			"invalid bucket_interval %q: must be a positive duration (e.g. 30s, 5m, 1h)", bucketInterval)), nil
	}

	// Resolve the env scope so these aggregates only span environments the
	// caller's token is authorized for (an empty Environment matches ALL envs).
	environment, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	now := time.Now().UTC()

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
	// Guard the trend loop before touching the store. A non-positive interval
	// never terminates, and even a valid one can demand tens of thousands of
	// sequential scans; both are refused with an actionable message rather than
	// silently pinning the process.
	if bucketDur <= 0 {
		return NewToolResultError("invalid bucket_interval: must be a positive duration (e.g. 30s, 5m, 1h)"), nil
	}
	span := until.Sub(since)
	if span <= 0 {
		return NewToolResultError("time window must start in the past"), nil
	}
	wantBuckets := int(span/bucketDur) + 1
	if wantBuckets > maxTrendBuckets {
		minInterval := (span/time.Duration(maxTrendBuckets) + time.Second).Truncate(time.Second)
		return NewToolResultError(fmt.Sprintf(
			"bucket_interval too small for this window: %s / %s = %d buckets (max %d). Use bucket_interval >= %s or a shorter window.",
			span.Round(time.Second), bucketDur, wantBuckets, maxTrendBuckets, minInterval)), nil
	}

	lc, err := svc.CountByLevel(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count logs: %v", err)), nil
	}

	// Build trend buckets.
	trend := make([]map[string]any, 0, wantBuckets)
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
			slog.Warn("logs_stats bucket count failed",
				"event", "logs_stats_bucket_failed",
				"bucket", t.Format(time.RFC3339),
				"error", err,
			)
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

	// CountByService is only used to enumerate the services present in the
	// window: its ServiceLogCount.ErrorCount is not populated by the production
	// adapter and its Total ignores the level filter. Both numbers are derived
	// per service from CountByLevel, which honours Service, Level and
	// Environment — otherwise every row reports errors: 0 and the level filter
	// silently does nothing.
	sort.Slice(services, func(i, j int) bool { return services[i].Total > services[j].Total })
	truncated := false
	if len(services) > maxServiceBreakdown {
		services = services[:maxServiceBreakdown]
		truncated = true
	}

	type serviceStat struct {
		name   string
		total  int
		errors int
	}
	stats := make([]serviceStat, 0, len(services))
	total := 0
	totalErrors := 0
	for _, s := range services {
		svcParams := params
		svcParams.Service = s.Service
		st := serviceStat{name: s.Service, total: s.Total, errors: s.ErrorCount}
		if lc, lcErr := svc.CountByLevel(ctx, svcParams); lcErr == nil {
			st.total = lc.Total
			st.errors = lc.ErrorCount
		} else {
			slog.Warn("logs_stats per-service level count failed",
				"event", "logs_stats_service_levels_failed",
				"service", s.Service,
				"error", lcErr,
			)
		}
		if st.total == 0 && st.errors == 0 {
			continue
		}
		total += st.total
		totalErrors += st.errors
		stats = append(stats, st)
	}

	byService := make([]map[string]any, 0, len(stats))
	for _, s := range stats {
		var errRate float64
		if s.total > 0 {
			errRate = float64(s.errors) / float64(s.total) * 100
		}
		byService = append(byService, map[string]any{
			"service":        s.name,
			"total":          s.total,
			"errors":         s.errors,
			"error_rate_pct": round2(errRate),
		})
	}

	var avgErrorRate float64
	if total > 0 {
		avgErrorRate = float64(totalErrors) / float64(total) * 100
	}

	var warnings []string
	for _, s := range stats {
		if s.total == 0 {
			continue
		}
		rate := float64(s.errors) / float64(s.total) * 100
		if avgErrorRate > 0 && rate > avgErrorRate*1.25 {
			warnings = append(warnings, fmt.Sprintf("Service '%s' error rate (%.1f%%) is above average (%.1f%%)", s.name, rate, avgErrorRate))
		}
	}
	if truncated {
		warnings = append(warnings, fmt.Sprintf(
			"Only the %d highest-volume services are broken down; total_logs covers those services only.", maxServiceBreakdown))
	}

	resp := map[string]any{
		"time_range":       map[string]any{"start": since.Format(time.RFC3339), "end": until.Format(time.RFC3339)},
		"total_logs":       total,
		"by_service":       byService,
		"services_covered": len(byService),
		"complete":         !truncated,
	}
	if params.Level != "" {
		resp["level_filter"] = params.Level
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	return JSONResult(resp)
}

func LogsStatsByPattern(ctx context.Context, svc *logs.Service, since, until time.Time, service, environment string) (*CallToolResult, error) {
	// Clustering runs over a *sample*: the log service clamps every search to
	// maxPatternSample rows. Asking for 10,000 did not raise that ceiling, it
	// only hid it — counts and percentages were then reported as if they covered
	// the whole window. Ask for exactly what the store will give, and report the
	// window's real error total separately so the sample is never mistaken for
	// the answer.
	errorResult, err := svc.Search(ctx, store.LogSearchParams{
		Level:       "error",
		Service:     service,
		Environment: environment,
		Start:       &since,
		End:         &until,
		Limit:       maxPatternSample,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
	}
	errorLogs := errorResult.Entries
	sampleTruncated := len(errorResult.Entries) >= maxPatternSample

	// Also fetch fatal logs.
	fatalResult, err := svc.Search(ctx, store.LogSearchParams{
		Level:       "fatal",
		Service:     service,
		Environment: environment,
		Start:       &since,
		End:         &until,
		Limit:       maxPatternSample,
	})
	if err == nil {
		errorLogs = append(errorLogs, fatalResult.Entries...)
		if len(fatalResult.Entries) >= maxPatternSample {
			sampleTruncated = true
		}
	}

	// True error volume for the window, independent of the sample.
	windowErrors := len(errorLogs)
	if lc, lcErr := svc.CountByLevel(ctx, store.LogCountParams{
		Since:       since,
		Until:       until,
		Service:     service,
		Environment: environment,
	}); lcErr == nil && lc.ErrorCount >= len(errorLogs) {
		windowErrors = lc.ErrorCount
	} else if lcErr != nil {
		slog.Warn("logs_stats pattern window count failed",
			"event", "logs_stats_pattern_count_failed",
			"error", lcErr,
		)
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

	sampleSize := len(errorLogs)
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

		var pctOfSample float64
		if sampleSize > 0 {
			pctOfSample = float64(sp.pd.count) / float64(sampleSize) * 100
		}

		entry := map[string]any{
			"pattern":        sp.key,
			"count":          sp.pd.count,
			"pct_of_sample":  round2(pctOfSample),
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
	if len(sorted) > 0 && sampleSize > 0 {
		topPct := float64(sorted[0].pd.count) / float64(sampleSize) * 100
		if topPct > 50 {
			warnings = append(warnings, fmt.Sprintf("Top error pattern '%s' accounts for %.0f%% of the analyzed sample", sorted[0].key, topPct))
		}
	}
	if sampleTruncated {
		warnings = append(warnings, fmt.Sprintf(
			"Pattern analysis covers the %d most recent error/fatal logs out of %d in the window — counts, percentages and first_seen describe that sample, not the whole window. Narrow the window or filter by service for full coverage.",
			sampleSize, windowErrors))
	}

	resp := map[string]any{
		"time_range":       map[string]any{"start": since.Format(time.RFC3339), "end": until.Format(time.RFC3339)},
		"total_errors":     windowErrors,
		"analyzed_sample":  sampleSize,
		"sample_truncated": sampleTruncated,
		"patterns":         patternEntries,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	return JSONResult(resp)
}
