package watcher

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// WatchMetrics computes metric values from existing LogStore methods.
type WatchMetrics struct {
	logStore store.LogStore
}

// NewWatchMetrics creates a new WatchMetrics helper.
func NewWatchMetrics(logStore store.LogStore) *WatchMetrics {
	return &WatchMetrics{logStore: logStore}
}

// Measure computes the given metric for the specified service/endpoint/env
// over the time window. environment scopes log-store queries to a single env
// — pass "" to match every env (used only in tests and legacy paths).
func (m *WatchMetrics) Measure(ctx context.Context, metric store.WatchMetric, service, endpoint, environment string, window time.Duration) (float64, error) {
	switch metric {
	case store.WatchMetricErrorRate:
		return m.measureErrorRate(ctx, service, environment, window)
	case store.WatchMetricResponseTime:
		return m.measureResponseTime(ctx, service, endpoint, environment, window)
	case store.WatchMetricP95Response:
		return m.measureP95Response(ctx, service, endpoint, environment, window)
	case store.WatchMetricLogCount:
		return m.measureLogCount(ctx, service, environment, window)
	case store.WatchMetricErrorCount:
		return m.measureErrorCount(ctx, service, environment, window)
	case store.WatchMetricHeartbeat:
		return m.measureHeartbeat(ctx, service, environment, window)
	case store.WatchMetricSQLCount:
		return m.measureSQLCount(ctx, service, endpoint, environment, window)
	case store.WatchMetricCacheHitRate:
		return m.measureCacheHitRate(ctx, service, endpoint, environment, window)
	default:
		return 0, fmt.Errorf("unknown metric: %s", metric)
	}
}

func (m *WatchMetrics) measureErrorRate(ctx context.Context, service, environment string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:       now.Add(-window),
		Until:       now,
		Service:     service,
		Environment: environment,
	})
	if err != nil {
		return 0, fmt.Errorf("counting by level: %w", err)
	}

	total := 0
	errors := 0
	for level, count := range counts {
		total += count
		if level == "error" || level == "fatal" {
			errors += count
		}
	}
	if total == 0 {
		return 0, nil
	}
	return float64(errors) / float64(total), nil
}

// measureResponseTime uses SQL aggregation — no row loading.
func (m *WatchMetrics) measureResponseTime(ctx context.Context, service, endpoint, environment string, window time.Duration) (float64, error) {
	agg, err := m.aggregate(ctx, service, endpoint, environment, window)
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
func (m *WatchMetrics) measureP95Response(ctx context.Context, service, endpoint, environment string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	start := now.Add(-window)

	params := store.RequestSummarySearchParams{
		Start:       &start,
		End:         &now,
		Environment: environment,
		SortBy:      "duration_ms",
		Limit:       200,
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
	idx := int(math.Ceil(float64(len(durations))*0.95)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(durations) {
		idx = len(durations) - 1
	}
	return durations[idx], nil
}

func (m *WatchMetrics) measureLogCount(ctx context.Context, service, environment string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:       now.Add(-window),
		Until:       now,
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

func (m *WatchMetrics) measureErrorCount(ctx context.Context, service, environment string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:       now.Add(-window),
		Until:       now,
		Service:     service,
		Level:       "error",
		Environment: environment,
	})
	if err != nil {
		return 0, fmt.Errorf("counting errors: %w", err)
	}

	total := 0
	for _, count := range counts {
		total += count
	}
	return float64(total), nil
}

func (m *WatchMetrics) measureHeartbeat(ctx context.Context, service, environment string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	start := now.Add(-window)
	entries, err := m.logStore.Search(ctx, store.LogSearchParams{
		Service:     service,
		Environment: environment,
		Start:       &start,
		End:         &now,
		Limit:       1,
	})
	if err != nil {
		return 0, fmt.Errorf("searching for heartbeat: %w", err)
	}
	if len(entries) == 0 {
		return window.Seconds(), nil
	}
	return now.Sub(entries[0].Timestamp).Seconds(), nil
}

// measureSQLCount uses SQL aggregation — no row loading.
func (m *WatchMetrics) measureSQLCount(ctx context.Context, service, endpoint, environment string, window time.Duration) (float64, error) {
	agg, err := m.aggregate(ctx, service, endpoint, environment, window)
	if err != nil {
		return 0, err
	}
	if agg.Count == 0 {
		return 0, nil
	}
	return agg.AvgSQLCount, nil
}

// measureCacheHitRate uses SQL aggregation — no row loading.
func (m *WatchMetrics) measureCacheHitRate(ctx context.Context, service, endpoint, environment string, window time.Duration) (float64, error) {
	agg, err := m.aggregate(ctx, service, endpoint, environment, window)
	if err != nil {
		return 0, err
	}
	if agg.TotalReads == 0 {
		return 0, nil
	}
	return agg.CacheHitRate, nil
}

// aggregate runs a single SQL aggregation query for response time, SQL count, and cache metrics.
func (m *WatchMetrics) aggregate(ctx context.Context, service, endpoint, environment string, window time.Duration) (*store.RequestSummaryAggregates, error) {
	now := time.Now().UTC()
	start := now.Add(-window)

	agg, err := m.logStore.AggregateRequestSummaries(ctx, store.RequestSummaryAggregateParams{
		Start:       &start,
		End:         &now,
		Service:     service,
		Endpoint:    endpoint,
		Environment: environment,
	})
	if err != nil {
		return nil, fmt.Errorf("aggregating summaries: %w", err)
	}
	return agg, nil
}
