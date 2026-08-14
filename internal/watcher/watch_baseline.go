package watcher

import (
	"context"
	"log/slog"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

const (
	// baselineErrorClassColumn is the log store's distinct-values column for
	// exception classes. The store calls it "error_class".
	baselineErrorClassColumn = "error_class"
	// baselineSummaryLimit bounds the request summaries loaded per baseline.
	baselineSummaryLimit = 1000
	// defaultBaselineWindow is used when a watch declares no baseline window.
	defaultBaselineWindow    = 1 * time.Hour
	defaultBaselineWindowStr = "1h"
)

// CaptureBaseline takes a snapshot of current metrics for a watch.
func CaptureBaseline(ctx context.Context, logStore store.LogStore, metrics *WatchMetrics, w *store.Watch) (*store.WatchBaseline, error) {
	windowStr := w.BaselineWindow
	if windowStr == "" {
		windowStr = defaultBaselineWindowStr
	}
	window := parseDurationOr(windowStr, defaultBaselineWindow)

	now := time.Now().UTC()
	start := now.Add(-window)

	baseline := &store.WatchBaseline{
		CapturedAt:     now,
		WindowDuration: windowStr,
	}

	// Every baseline query is scoped to the watch's environment — the live
	// measurements are, and a cross-env baseline makes relative conditions
	// compare a production value against a blend of production and staging.
	countParams := store.LogCountParams{
		Since:       start,
		Until:       now,
		Service:     w.Service,
		Environment: w.Environment,
	}

	// Error rate
	counts, err := logStore.CountByLevel(ctx, countParams)
	if err != nil {
		slog.Warn("baseline: counting logs by level", "watch_id", w.ID, "error", err)
	} else {
		total := 0
		errorCount := 0
		for level, count := range counts {
			total += count
			if isErrorLevel(level) {
				errorCount += count
			}
		}
		baseline.LogCount = total
		baseline.ErrorCount = errorCount
		if total > 0 {
			baseline.ErrorRate = float64(errorCount) / float64(total)
		}
	}

	// Exception classes. The log store's column is "error_class" — querying
	// "exception_class" always errored, leaving this nil so every error in
	// alert evidence was later reported as brand new.
	classes, err := logStore.DistinctValues(ctx, baselineErrorClassColumn, countParams)
	if err != nil {
		slog.Warn("baseline: listing error classes", "watch_id", w.ID, "error", err)
	} else {
		baseline.ExceptionClasses = classes
	}

	// Request summaries for response time and endpoint stats
	summaries, err := logStore.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start:       &start,
		End:         &now,
		Environment: w.Environment,
		Limit:       baselineSummaryLimit,
	})
	if err != nil {
		slog.Warn("baseline: searching request summaries", "watch_id", w.ID, "error", err)
	} else {
		var filteredSummaries []store.RequestSummaryResult
		for _, s := range summaries {
			if w.Service == "" || s.Service == w.Service {
				filteredSummaries = append(filteredSummaries, s)
			}
		}

		if len(filteredSummaries) > 0 {
			// Avg response time
			var totalDur float64
			var totalSQL float64
			var totalCacheReads, totalCacheHits int
			for _, s := range filteredSummaries {
				totalDur += s.DurationMs
				totalSQL += float64(s.SQLCount)
				totalCacheReads += s.CacheReads
				totalCacheHits += s.CacheHits
			}
			n := float64(len(filteredSummaries))
			baseline.AvgResponseMs = totalDur / n
			baseline.SQLCount = totalSQL / n
			if totalCacheReads > 0 {
				baseline.CacheHitRate = float64(totalCacheHits) / float64(totalCacheReads)
			}

			// P95
			val, err := metrics.Measure(ctx, store.WatchMetricP95Response, w.Service, w.Endpoint, w.Environment, window)
			if err == nil {
				baseline.P95ResponseMs = val
			}

			// Per-endpoint stats
			endpointStats := make(map[string]struct {
				totalMs float64
				totalSQ float64
				count   int
			})
			for _, s := range filteredSummaries {
				entry := endpointStats[s.Path]
				entry.totalMs += s.DurationMs
				entry.totalSQ += float64(s.SQLCount)
				entry.count++
				endpointStats[s.Path] = entry
			}
			for path, stats := range endpointStats {
				if stats.count > 0 {
					baseline.Endpoints = append(baseline.Endpoints, store.WatchEndpointBaseline{
						Path:          path,
						AvgDurationMs: stats.totalMs / float64(stats.count),
						AvgSQLCount:   stats.totalSQ / float64(stats.count),
						RequestCount:  stats.count,
					})
				}
			}
		}
	}

	return baseline, nil
}
