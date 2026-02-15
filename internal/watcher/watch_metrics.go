package watcher

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// WatchMetrics computes metric values from existing LogStore methods.
type WatchMetrics struct {
	logStore store.LogStore
}

// NewWatchMetrics creates a new WatchMetrics helper.
func NewWatchMetrics(logStore store.LogStore) *WatchMetrics {
	return &WatchMetrics{logStore: logStore}
}

// Measure computes the given metric for the specified service/endpoint over the time window.
func (m *WatchMetrics) Measure(ctx context.Context, metric store.WatchMetric, service, endpoint string, window time.Duration) (float64, error) {
	switch metric {
	case store.WatchMetricErrorRate:
		return m.measureErrorRate(ctx, service, window)
	case store.WatchMetricResponseTime:
		return m.measureResponseTime(ctx, service, endpoint, window)
	case store.WatchMetricP95Response:
		return m.measureP95Response(ctx, service, endpoint, window)
	case store.WatchMetricLogCount:
		return m.measureLogCount(ctx, service, window)
	case store.WatchMetricErrorCount:
		return m.measureErrorCount(ctx, service, window)
	case store.WatchMetricHeartbeat:
		return m.measureHeartbeat(ctx, service, window)
	case store.WatchMetricSQLCount:
		return m.measureSQLCount(ctx, service, endpoint, window)
	case store.WatchMetricCacheHitRate:
		return m.measureCacheHitRate(ctx, service, endpoint, window)
	default:
		return 0, fmt.Errorf("unknown metric: %s", metric)
	}
}

func (m *WatchMetrics) measureErrorRate(ctx context.Context, service string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:   now.Add(-window),
		Until:   now,
		Service: service,
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

func (m *WatchMetrics) measureResponseTime(ctx context.Context, service, endpoint string, window time.Duration) (float64, error) {
	summaries, err := m.getRequestSummaries(ctx, service, endpoint, window)
	if err != nil {
		return 0, err
	}
	if len(summaries) == 0 {
		return 0, nil
	}

	var total float64
	for _, s := range summaries {
		total += s.DurationMs
	}
	return total / float64(len(summaries)), nil
}

func (m *WatchMetrics) measureP95Response(ctx context.Context, service, endpoint string, window time.Duration) (float64, error) {
	summaries, err := m.getRequestSummaries(ctx, service, endpoint, window)
	if err != nil {
		return 0, err
	}
	if len(summaries) == 0 {
		return 0, nil
	}

	durations := make([]float64, len(summaries))
	for i, s := range summaries {
		durations[i] = s.DurationMs
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

func (m *WatchMetrics) measureLogCount(ctx context.Context, service string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:   now.Add(-window),
		Until:   now,
		Service: service,
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

func (m *WatchMetrics) measureErrorCount(ctx context.Context, service string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	counts, err := m.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:   now.Add(-window),
		Until:   now,
		Service: service,
		Level:   "error",
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

func (m *WatchMetrics) measureHeartbeat(ctx context.Context, service string, window time.Duration) (float64, error) {
	now := time.Now().UTC()
	start := now.Add(-window)
	entries, err := m.logStore.Search(ctx, store.LogSearchParams{
		Service: service,
		Start:   &start,
		End:     &now,
		Limit:   1,
	})
	if err != nil {
		return 0, fmt.Errorf("searching for heartbeat: %w", err)
	}
	if len(entries) == 0 {
		// No logs in window — return seconds since window start (high = no heartbeat)
		return window.Seconds(), nil
	}
	// Return seconds since last log (low = healthy)
	return now.Sub(entries[0].Timestamp).Seconds(), nil
}

func (m *WatchMetrics) measureSQLCount(ctx context.Context, service, endpoint string, window time.Duration) (float64, error) {
	summaries, err := m.getRequestSummaries(ctx, service, endpoint, window)
	if err != nil {
		return 0, err
	}
	if len(summaries) == 0 {
		return 0, nil
	}

	var total float64
	for _, s := range summaries {
		total += float64(s.SQLCount)
	}
	return total / float64(len(summaries)), nil
}

func (m *WatchMetrics) measureCacheHitRate(ctx context.Context, service, endpoint string, window time.Duration) (float64, error) {
	summaries, err := m.getRequestSummaries(ctx, service, endpoint, window)
	if err != nil {
		return 0, err
	}
	if len(summaries) == 0 {
		return 0, nil
	}

	var totalReads, totalHits int
	for _, s := range summaries {
		totalReads += s.CacheReads
		totalHits += s.CacheHits
	}
	if totalReads == 0 {
		return 0, nil
	}
	return float64(totalHits) / float64(totalReads), nil
}

func (m *WatchMetrics) getRequestSummaries(ctx context.Context, service, endpoint string, window time.Duration) ([]store.RequestSummaryResult, error) {
	now := time.Now().UTC()
	start := now.Add(-window)

	params := store.RequestSummarySearchParams{
		Start: &start,
		End:   &now,
		Limit: 1000,
	}
	if endpoint != "" {
		params.Path = endpoint
	}

	summaries, err := m.logStore.SearchRequestSummaries(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("searching request summaries: %w", err)
	}

	// Filter by service if specified (SearchRequestSummaries doesn't have service filter)
	if service != "" {
		filtered := make([]store.RequestSummaryResult, 0, len(summaries))
		for _, s := range summaries {
			if s.Service == service {
				filtered = append(filtered, s)
			}
		}
		return filtered, nil
	}
	return summaries, nil
}
