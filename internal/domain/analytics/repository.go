// Package analytics provides the domain service for traffic analytics,
// endpoint ranking, heatmaps, and trend analysis.
package analytics

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// AnalyticsRepository defines data access for traffic analytics.
// Implemented by internal/db.AnalyticsStore (SQLite adapter).
type AnalyticsRepository interface {
	TrafficSummary(ctx context.Context, params store.AnalyticsParams) (*store.TrafficSummary, error)
	TopEndpoints(ctx context.Context, params store.TopEndpointParams) ([]store.EndpointStat, error)
	TrafficHeatmap(ctx context.Context, service string) ([]store.HeatmapCell, error)
}

// TrendRepository defines data access for metric trend queries.
// Implemented by internal/db.TrendStore (SQLite adapter).
type TrendRepository interface {
	QueryTrends(ctx context.Context, params store.TrendQueryParams) ([]store.MetricBucket, error)
	ListDeployMarkers(ctx context.Context, service string, since time.Time) ([]store.DeployMarker, error)
}
