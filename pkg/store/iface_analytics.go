package store

import (
	"context"
	"time"
)

// TrendStore manages pre-aggregated metric buckets and deploy markers.
type TrendStore interface {
	// Aggregation (background job)
	AggregateBuckets(ctx context.Context, interval string, since time.Time) error

	// Query
	QueryTrends(ctx context.Context, params TrendQueryParams) ([]MetricBucket, error)
	ListDeployMarkers(ctx context.Context, service string, since time.Time) ([]DeployMarker, error)

	// Maintenance
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// AnalyticsStore manages web analytics data: endpoint stats, traffic heatmaps.
type AnalyticsStore interface {
	// Aggregation (background job)
	AggregateEndpointStats(ctx context.Context, period string, since time.Time) error
	UpdateTrafficHeatmap(ctx context.Context, since time.Time) error

	// Query
	TopEndpoints(ctx context.Context, params TopEndpointParams) ([]EndpointStat, error)
	TrafficSummary(ctx context.Context, params AnalyticsParams) (*TrafficSummary, error)
	TrafficHeatmap(ctx context.Context, service string) ([]HeatmapCell, error)

	// Maintenance
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
