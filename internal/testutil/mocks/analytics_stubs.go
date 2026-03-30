package mocks

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.AnalyticsStore = (*AnalyticsStore)(nil)
var _ store.TrendStore = (*TrendStore)(nil)

// ===========================================================================
// TrendStore
// ===========================================================================

// TrendStore is a stub implementing store.TrendStore.
type TrendStore struct{}

// NewTrendStore returns an initialised TrendStore stub.
func NewTrendStore() *TrendStore { return &TrendStore{} }

func (m *TrendStore) AggregateBuckets(_ context.Context, _ string, _ time.Time) error { return nil }
func (m *TrendStore) QueryTrends(_ context.Context, _ store.TrendQueryParams) ([]store.MetricBucket, error) {
	return nil, nil
}
func (m *TrendStore) ListDeployMarkers(_ context.Context, _ string, _ time.Time) ([]store.DeployMarker, error) {
	return nil, nil
}
func (m *TrendStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// AnalyticsStore
// ===========================================================================

// AnalyticsStore is a stub implementing store.AnalyticsStore.
type AnalyticsStore struct{}

// NewAnalyticsStore returns an initialised AnalyticsStore stub.
func NewAnalyticsStore() *AnalyticsStore { return &AnalyticsStore{} }

func (m *AnalyticsStore) AggregateEndpointStats(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (m *AnalyticsStore) UpdateTrafficHeatmap(_ context.Context, _ time.Time) error { return nil }
func (m *AnalyticsStore) TopEndpoints(_ context.Context, _ store.TopEndpointParams) ([]store.EndpointStat, error) {
	return nil, nil
}
func (m *AnalyticsStore) TrafficSummary(_ context.Context, _ store.AnalyticsParams) (*store.TrafficSummary, error) {
	return &store.TrafficSummary{}, nil
}
func (m *AnalyticsStore) TrafficHeatmap(_ context.Context, _ string) ([]store.HeatmapCell, error) {
	return nil, nil
}
func (m *AnalyticsStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
