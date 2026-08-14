package watcher

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// p95Percentile is the percentile measureP95Response computes.
const p95Percentile = 0.95

// p95SampleLimit bounds how many request summaries are loaded for the
// percentile calculation (SQLite has no native percentile function).
const p95SampleLimit = 200

// WatchMetrics computes metric values from existing LogStore methods.
type WatchMetrics struct {
	logStore store.LogStore
}

// NewWatchMetrics creates a new WatchMetrics helper.
func NewWatchMetrics(logStore store.LogStore) *WatchMetrics {
	return &WatchMetrics{logStore: logStore}
}

// Measure computes the given metric for the specified service/endpoint/env
// over the window ending now. environment scopes log-store queries to a single
// env — pass "" to match every env (used only in tests and legacy paths).
func (m *WatchMetrics) Measure(ctx context.Context, metric store.WatchMetric, service, endpoint, environment string, window time.Duration) (float64, error) {
	end := time.Now().UTC()
	return m.MeasureRange(ctx, metric, service, endpoint, environment, end.Add(-window), end)
}

// MeasureRange computes the given metric over an explicit [start, end) range.
// Delta conditions need a window that does not end at "now", so every metric is
// expressed in terms of an absolute range and Measure is the "ending now" case.
func (m *WatchMetrics) MeasureRange(ctx context.Context, metric store.WatchMetric, service, endpoint, environment string, start, end time.Time) (float64, error) {
	switch metric {
	case store.WatchMetricErrorRate:
		return m.measureErrorRate(ctx, service, environment, start, end)
	case store.WatchMetricResponseTime:
		return m.measureResponseTime(ctx, service, endpoint, environment, start, end)
	case store.WatchMetricP95Response:
		return m.measureP95Response(ctx, service, endpoint, environment, start, end)
	case store.WatchMetricLogCount:
		return m.measureLogCount(ctx, service, environment, start, end)
	case store.WatchMetricErrorCount:
		return m.measureErrorCount(ctx, service, environment, start, end)
	case store.WatchMetricHeartbeat:
		return m.measureHeartbeat(ctx, service, environment, start, end)
	case store.WatchMetricSQLCount:
		return m.measureSQLCount(ctx, service, endpoint, environment, start, end)
	case store.WatchMetricCacheHitRate:
		// The segmented log store's flat schema does not carry cache_reads /
		// cache_hits, so AggregateRequestSummaries can never populate them.
		// Returning 0 made every "cache_hit_rate lt X" watch breach on every
		// check forever; fail loudly instead so the watch is disabled.
		return 0, configErrorf("metric %q is not supported by this log store (cache counters are not persisted)", metric)
	default:
		return 0, configErrorf("unknown metric: %s", metric)
	}
}

// isErrorLevel reports whether a log level counts as an error. Fatal counts as
// an error everywhere — error_rate, error_count and the captured baseline must
// agree, or relative conditions compare incompatible numbers.
func isErrorLevel(level string) bool {
	switch strings.ToLower(level) {
	case "error", "fatal":
		return true
	default:
		return false
	}
}

func (m *WatchMetrics) measureErrorRate(ctx context.Context, service, environment string, start, end time.Time) (float64, error) {
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:       start,
		Until:       end,
		Service:     service,
		Environment: environment,
	})
	if err != nil {
		return 0, fmt.Errorf("counting by level: %w", err)
	}

	total := 0
	errorCount := 0
	for level, count := range counts {
		total += count
		if isErrorLevel(level) {
			errorCount += count
		}
	}
	if total == 0 {
		return 0, nil
	}
	return float64(errorCount) / float64(total), nil
}

// measureResponseTime uses SQL aggregation — no row loading.
func (m *WatchMetrics) measureResponseTime(ctx context.Context, service, endpoint, environment string, start, end time.Time) (float64, error) {
	agg, err := m.aggregate(ctx, service, endpoint, environment, start, end)
	if err != nil {
		return 0, err
	}
	if agg.Count == 0 {
		return 0, nil
	}
	return agg.AvgDuration, nil
}

// measureP95Response loads a limited set of durations for percentile calculation.
// SQLite has no native percentile function, so we sort in Go.
func (m *WatchMetrics) measureP95Response(ctx context.Context, service, endpoint, environment string, start, end time.Time) (float64, error) {
	params := store.RequestSummarySearchParams{
		Start:       &start,
		End:         &end,
		Environment: environment,
		SortBy:      "duration_ms",
		Limit:       p95SampleLimit,
	}
	if endpoint != "" {
		params.Path = endpoint
	}

	summaries, err := m.logStore.SearchRequestSummaries(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("searching for P95: %w", err)
	}

	// Filter by service in Go (SearchRequestSummaries has no service param).
	var durations []float64
	for _, s := range summaries {
		if service == "" || s.Service == service {
			durations = append(durations, s.DurationMs)
		}
	}
	if len(durations) == 0 {
		return 0, nil
	}

	sort.Float64s(durations)
	idx := int(math.Ceil(float64(len(durations))*p95Percentile)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(durations) {
		idx = len(durations) - 1
	}
	return durations[idx], nil
}

func (m *WatchMetrics) measureLogCount(ctx context.Context, service, environment string, start, end time.Time) (float64, error) {
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:       start,
		Until:       end,
		Service:     service,
		Environment: environment,
	})
	if err != nil {
		return 0, fmt.Errorf("counting by level: %w", err)
	}

	total := 0
	for _, count := range counts {
		total += count
	}
	return float64(total), nil
}

// measureErrorCount counts error *and* fatal entries. CountByLevel's Level
// filter only accepts a single level, so we count every level and sum the
// error-ish ones — the same rule error_rate and CaptureBaseline use.
func (m *WatchMetrics) measureErrorCount(ctx context.Context, service, environment string, start, end time.Time) (float64, error) {
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:       start,
		Until:       end,
		Service:     service,
		Environment: environment,
	})
	if err != nil {
		return 0, fmt.Errorf("counting errors: %w", err)
	}

	total := 0
	for level, count := range counts {
		if isErrorLevel(level) {
			total += count
		}
	}
	return float64(total), nil
}

func (m *WatchMetrics) measureHeartbeat(ctx context.Context, service, environment string, start, end time.Time) (float64, error) {
	entries, err := m.logStore.Search(ctx, store.LogSearchParams{
		Service:     service,
		Environment: environment,
		Start:       &start,
		End:         &end,
		Limit:       1,
	})
	if err != nil {
		return 0, fmt.Errorf("searching for heartbeat: %w", err)
	}
	if len(entries) == 0 {
		return end.Sub(start).Seconds(), nil
	}
	return end.Sub(entries[0].Timestamp).Seconds(), nil
}

// measureSQLCount uses SQL aggregation — no row loading.
func (m *WatchMetrics) measureSQLCount(ctx context.Context, service, endpoint, environment string, start, end time.Time) (float64, error) {
	agg, err := m.aggregate(ctx, service, endpoint, environment, start, end)
	if err != nil {
		return 0, err
	}
	if agg.Count == 0 {
		return 0, nil
	}
	return agg.AvgSQLCount, nil
}

// aggregate runs a single SQL aggregation query for response time and SQL count.
func (m *WatchMetrics) aggregate(ctx context.Context, service, endpoint, environment string, start, end time.Time) (*store.RequestSummaryAggregates, error) {
	agg, err := m.logStore.AggregateRequestSummaries(ctx, store.RequestSummaryAggregateParams{
		Start:       &start,
		End:         &end,
		Service:     service,
		Endpoint:    endpoint,
		Environment: environment,
	})
	if err != nil {
		return nil, fmt.Errorf("aggregating summaries: %w", err)
	}
	return agg, nil
}
