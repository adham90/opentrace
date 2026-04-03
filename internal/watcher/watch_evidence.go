package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// WatchEvidenceBuilder collects evidence when a watch alert fires.
type WatchEvidenceBuilder struct {
	logStore store.LogStore
	metrics  *WatchMetrics
}

// NewWatchEvidenceBuilder creates a new evidence builder.
func NewWatchEvidenceBuilder(logStore store.LogStore, metrics *WatchMetrics) *WatchEvidenceBuilder {
	return &WatchEvidenceBuilder{
		logStore: logStore,
		metrics:  metrics,
	}
}

// Build collects all evidence for a triggered watch.
func (b *WatchEvidenceBuilder) Build(ctx context.Context, w *store.Watch, result *WatchEvalResult) (*store.WatchEvidenceBundle, error) {
	now := time.Now().UTC()
	window, err := time.ParseDuration(w.BaselineWindow)
	if err != nil {
		window = 1 * time.Hour
	}
	start := now.Add(-window)

	evidence := &store.WatchEvidenceBundle{}

	// 1. Recent errors
	recentErrors, err := b.logStore.Search(ctx, store.LogSearchParams{
		Service: w.Service,
		Level:   "error",
		Start:   &start,
		End:     &now,
		Limit:   20,
	})
	if err == nil {
		errorCounts := make(map[string]*store.WatchEvidenceError)
		for _, entry := range recentErrors {
			cls := entry.ExceptionClass
			if cls == "" {
				cls = "Unknown"
			}
			if existing, ok := errorCounts[cls]; ok {
				existing.Count++
			} else {
				errorCounts[cls] = &store.WatchEvidenceError{
					ExceptionClass: cls,
					Message:        truncate(entry.Message, 200),
					Count:          1,
				}
			}
		}

		// Mark new errors (not in baseline)
		var baselineClasses map[string]bool
		if w.BaselineJSON != nil {
			baselineClasses = make(map[string]bool, len(w.BaselineJSON.ExceptionClasses))
			for _, cls := range w.BaselineJSON.ExceptionClasses {
				baselineClasses[cls] = true
			}
		}

		for _, e := range errorCounts {
			if baselineClasses != nil && !baselineClasses[e.ExceptionClass] {
				e.IsNew = true
				evidence.NewErrors = append(evidence.NewErrors, *e)
			}
			evidence.RecentErrors = append(evidence.RecentErrors, *e)
		}
	}

	// 2. Affected endpoints
	summaries, err := b.logStore.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: &start,
		End:   &now,
		Limit: 100,
	})
	if err == nil && w.BaselineJSON != nil {
		baselineByPath := make(map[string]store.WatchEndpointBaseline)
		for _, ep := range w.BaselineJSON.Endpoints {
			baselineByPath[ep.Path] = ep
		}

		currentByPath := make(map[string]struct {
			totalMs float64
			count   int
		})
		for _, s := range summaries {
			if w.Service != "" && s.Service != w.Service {
				continue
			}
			entry := currentByPath[s.Path]
			entry.totalMs += s.DurationMs
			entry.count++
			currentByPath[s.Path] = entry
		}

		for path, current := range currentByPath {
			if current.count == 0 {
				continue
			}
			avgMs := current.totalMs / float64(current.count)
			if baseline, ok := baselineByPath[path]; ok && baseline.AvgDurationMs > 0 {
				deltaPct := ((avgMs - baseline.AvgDurationMs) / baseline.AvgDurationMs) * 100
				if deltaPct > 10 { // Only report endpoints that degraded >10%
					evidence.AffectedEndpoints = append(evidence.AffectedEndpoints, store.WatchEndpointDelta{
						Path:               path,
						CurrentDurationMs:  avgMs,
						BaselineDurationMs: baseline.AvgDurationMs,
						DeltaPct:           deltaPct,
					})
				}
			}
		}
	}

	// 3. Relevant logs (most recent)
	recentLogs, err := b.logStore.Search(ctx, store.LogSearchParams{
		Service: w.Service,
		Start:   &start,
		End:     &now,
		Limit:   20,
	})
	if err == nil {
		for _, entry := range recentLogs {
			evidence.RelevantLogs = append(evidence.RelevantLogs, store.WatchEvidenceLog{
				Timestamp: entry.Timestamp,
				Level:     entry.Level,
				Message:   truncate(entry.Message, 200),
				Service:   entry.Service,
				TraceID:   entry.TraceID,
			})
		}
	}

	// 4. Source files and trace IDs from log entries
	sourceFileSet := make(map[string]bool)
	traceIDSet := make(map[string]bool)
	for _, entry := range recentErrors {
		if entry.SourceFile != "" {
			sourceFileSet[entry.SourceFile] = true
		}
		if entry.TraceID != "" {
			traceIDSet[entry.TraceID] = true
		}
	}
	for f := range sourceFileSet {
		evidence.SourceFiles = append(evidence.SourceFiles, f)
	}
	for id := range traceIDSet {
		evidence.TraceIDs = append(evidence.TraceIDs, id)
	}

	// 5. Baseline diff
	if w.BaselineJSON != nil {
		metric := store.ConditionsMetric(w.ConditionsJSON)
		metricName := store.ConditionsSummary(w.ConditionsJSON)
		bVal := baselineValueForMetric(w.BaselineJSON, metric)
		evidence.BaselineDiff = &store.WatchBaselineDiff{
			MetricName:    metricName,
			BaselineValue: bVal,
			CurrentValue:  result.Value,
		}
		if evidence.BaselineDiff.BaselineValue != 0 {
			evidence.BaselineDiff.DeltaPct = ((result.Value - evidence.BaselineDiff.BaselineValue) / evidence.BaselineDiff.BaselineValue) * 100
		}
	}

	// 6. Timeline
	evidence.Timeline = append(evidence.Timeline, store.WatchTimelineEvent{
		Timestamp: w.CreatedAt,
		Event:     "watch_created",
	})
	if w.BaselineJSON != nil {
		evidence.Timeline = append(evidence.Timeline, store.WatchTimelineEvent{
			Timestamp: w.BaselineJSON.CapturedAt,
			Event:     "baseline_captured",
		})
	}
	val := result.Value
	evidence.Timeline = append(evidence.Timeline, store.WatchTimelineEvent{
		Timestamp: now,
		Event:     fmt.Sprintf("alert_triggered: %s = %.4f", store.ConditionsSummary(w.ConditionsJSON), result.Value),
		Value:     &val,
	})

	return evidence, nil
}


func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
