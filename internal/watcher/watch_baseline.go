package watcher

import (
	"context"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// CaptureBaseline takes a snapshot of current metrics for a watch.
func CaptureBaseline(ctx context.Context, logStore store.LogStore, metrics *WatchMetrics, w *store.Watch) (*store.WatchBaseline, error) {
	windowStr := w.BaselineWindow
	if windowStr == "" {
		windowStr = "1h"
	}
	window, err := time.ParseDuration(windowStr)
	if err != nil {
		window = 1 * time.Hour
	}

	now := time.Now().UTC()
	start := now.Add(-window)

	baseline := &store.WatchBaseline{
		CapturedAt:     now,
		WindowDuration: windowStr,
	}

	// Error rate
	counts, err := logStore.CountByLevel(ctx, store.LogCountParams{
		Since:   start,
		Until:   now,
		Service: w.Service,
	})
	if err == nil {
		total := 0
		errors := 0
		for level, count := range counts {
			total += count
			if level == "error" || level == "fatal" {
				errors += count
			}
		}
		baseline.LogCount = total
		baseline.ErrorCount = errors
		if total > 0 {
			baseline.ErrorRate = float64(errors) / float64(total)
		}
	}

	// Exception classes
	classes, err := logStore.DistinctValues(ctx, "exception_class", store.LogCountParams{
		Since:   start,
		Until:   now,
		Service: w.Service,
	})
	if err == nil {
		baseline.ExceptionClasses = classes
	}

	// Request summaries for response time and endpoint stats
	summaries, err := logStore.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: &start,
		End:   &now,
		Limit: 1000,
	})
	if err == nil {
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
			val, err := metrics.Measure(ctx, store.WatchMetricP95Response, w.Service, w.Endpoint, window)
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
