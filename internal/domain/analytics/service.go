package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides traffic analytics, endpoint ranking, and trend analysis.
type Service struct {
	repo  AnalyticsRepository
	trend TrendRepository
}

// NewService creates an analytics service.
func NewService(repo AnalyticsRepository, trend TrendRepository) *Service {
	return &Service{repo: repo, trend: trend}
}

// Traffic returns a high-level traffic summary for the given time window.
func (s *Service) Traffic(ctx context.Context, params store.AnalyticsParams) (*store.TrafficSummary, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("traffic summary: %w", domain.ErrNotConfigured)
	}
	summary, err := s.repo.TrafficSummary(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("fetching traffic summary: %w", err)
	}
	return summary, nil
}

// Endpoints returns the top endpoints ranked by the specified criteria.
func (s *Service) Endpoints(ctx context.Context, params store.TopEndpointParams) ([]store.EndpointStat, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("top endpoints: %w", domain.ErrNotConfigured)
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}
	if params.Limit > 200 {
		params.Limit = 200
	}
	stats, err := s.repo.TopEndpoints(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("fetching top endpoints: %w", err)
	}
	return stats, nil
}

// Heatmap returns the 24x7 traffic heatmap for a service.
func (s *Service) Heatmap(ctx context.Context, service string) ([]store.HeatmapCell, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("traffic heatmap: %w", domain.ErrNotConfigured)
	}
	cells, err := s.repo.TrafficHeatmap(ctx, service)
	if err != nil {
		return nil, fmt.Errorf("fetching heatmap: %w", err)
	}
	return cells, nil
}

// Trends returns pre-aggregated metric buckets for the given query.
func (s *Service) Trends(ctx context.Context, params store.TrendQueryParams) ([]store.MetricBucket, error) {
	if s.trend == nil {
		return nil, fmt.Errorf("trends: %w", domain.ErrNotConfigured)
	}
	buckets, err := s.trend.QueryTrends(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("querying trends: %w", err)
	}
	return buckets, nil
}

// Movers returns deploy markers for a service since the given time.
func (s *Service) Movers(ctx context.Context, service string, since time.Time) ([]store.DeployMarker, error) {
	if s.trend == nil {
		return nil, fmt.Errorf("deploy markers: %w", domain.ErrNotConfigured)
	}
	markers, err := s.trend.ListDeployMarkers(ctx, service, since)
	if err != nil {
		return nil, fmt.Errorf("listing deploy markers: %w", err)
	}
	return markers, nil
}
