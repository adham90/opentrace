package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fakeAnalytics implements AnalyticsRepository for testing.
type fakeAnalytics struct {
	summary   *store.TrafficSummary
	endpoints []store.EndpointStat
	heatmap   []store.HeatmapCell
	err       error
}

func (f *fakeAnalytics) TrafficSummary(_ context.Context, _ store.AnalyticsParams) (*store.TrafficSummary, error) {
	return f.summary, f.err
}

func (f *fakeAnalytics) TopEndpoints(_ context.Context, _ store.TopEndpointParams) ([]store.EndpointStat, error) {
	return f.endpoints, f.err
}

func (f *fakeAnalytics) TrafficHeatmap(_ context.Context, _ string) ([]store.HeatmapCell, error) {
	return f.heatmap, f.err
}

var _ AnalyticsRepository = (*fakeAnalytics)(nil)

// fakeTrend implements TrendRepository for testing.
type fakeTrend struct {
	buckets []store.MetricBucket
	markers []store.DeployMarker
	err     error
}

func (f *fakeTrend) QueryTrends(_ context.Context, _ store.TrendQueryParams) ([]store.MetricBucket, error) {
	return f.buckets, f.err
}

func (f *fakeTrend) ListDeployMarkers(_ context.Context, _ string, _ time.Time) ([]store.DeployMarker, error) {
	return f.markers, f.err
}

var _ TrendRepository = (*fakeTrend)(nil)

// --- Traffic ---

func TestTraffic_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Traffic(context.Background(), store.AnalyticsParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestTraffic_HappyPath(t *testing.T) {
	f := &fakeAnalytics{
		summary: &store.TrafficSummary{
			TotalRequests:   1000,
			UniqueEndpoints: 10,
			ErrorRate:       2.5,
		},
	}
	svc := NewService(f, nil)
	result, err := svc.Traffic(context.Background(), store.AnalyticsParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalRequests != 1000 {
		t.Errorf("total_requests = %d, want 1000", result.TotalRequests)
	}
}

func TestTraffic_StoreError(t *testing.T) {
	f := &fakeAnalytics{err: errors.New("db down")}
	svc := NewService(f, nil)
	_, err := svc.Traffic(context.Background(), store.AnalyticsParams{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Endpoints ---

func TestEndpoints_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Endpoints(context.Background(), store.TopEndpointParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestEndpoints_HappyPath(t *testing.T) {
	f := &fakeAnalytics{
		endpoints: []store.EndpointStat{
			{PathPattern: "/api/users", RequestCount: 500},
		},
	}
	svc := NewService(f, nil)
	result, err := svc.Endpoints(context.Background(), store.TopEndpointParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("got %d endpoints, want 1", len(result))
	}
}

// --- Heatmap ---

func TestHeatmap_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Heatmap(context.Background(), "")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

// --- Trends ---

func TestTrends_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Trends(context.Background(), store.TrendQueryParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestTrends_HappyPath(t *testing.T) {
	f := &fakeTrend{
		buckets: []store.MetricBucket{
			{RequestCount: 100, AvgDurationMs: 42.5},
		},
	}
	svc := NewService(nil, f)
	result, err := svc.Trends(context.Background(), store.TrendQueryParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("got %d buckets, want 1", len(result))
	}
}

// --- Movers ---

func TestMovers_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Movers(context.Background(), "", time.Now())
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestMovers_HappyPath(t *testing.T) {
	f := &fakeTrend{
		markers: []store.DeployMarker{
			{Service: "api", CommitHash: "abc123"},
		},
	}
	svc := NewService(nil, f)
	result, err := svc.Movers(context.Background(), "api", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("got %d markers, want 1", len(result))
	}
}
