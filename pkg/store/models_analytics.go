package store

import (
	"time"

	"github.com/uptrace/bun"
)

// MetricBucket holds pre-aggregated metrics for a time bucket.
type MetricBucket struct {
	bun.BaseModel     `bun:"table:metric_buckets" json:"-"`
	ID                int64     `bun:"id,pk,autoincrement" json:"id"`
	BucketStart       time.Time `bun:"bucket_start" json:"bucket_start"`
	BucketInterval    string    `bun:"bucket_interval" json:"bucket_interval"`
	Service           string    `bun:"service" json:"service,omitempty"`
	Endpoint          string    `bun:"endpoint" json:"endpoint,omitempty"`
	Environment       string    `bun:"environment" json:"environment,omitempty"`
	RequestCount      int       `bun:"request_count" json:"request_count"`
	ErrorCount        int       `bun:"error_count" json:"error_count"`
	LogCount          int       `bun:"log_count" json:"log_count"`
	AvgDurationMs     float64   `bun:"avg_duration_ms" json:"avg_duration_ms"`
	P50DurationMs     float64   `bun:"p50_duration_ms" json:"p50_duration_ms"`
	P95DurationMs     float64   `bun:"p95_duration_ms" json:"p95_duration_ms"`
	P99DurationMs     float64   `bun:"p99_duration_ms" json:"p99_duration_ms"`
	MaxDurationMs     float64   `bun:"max_duration_ms" json:"max_duration_ms"`
	AvgSQLCount       float64   `bun:"avg_sql_count" json:"avg_sql_count"`
	AvgDBTimeMs       float64   `bun:"avg_db_time_ms" json:"avg_db_time_ms"`
	AvgCacheHitRatio  float64   `bun:"avg_cache_hit_ratio" json:"avg_cache_hit_ratio"`
	AvgHTTPExternalMs float64   `bun:"avg_http_external_ms" json:"avg_http_external_ms"`
	CreatedAt         time.Time `bun:"created_at" json:"created_at"`
}

// TrendQueryParams defines filters for querying trend data.
type TrendQueryParams struct {
	Service     string    `json:"service,omitempty"`
	Endpoint    string    `json:"endpoint,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Interval    string    `json:"interval,omitempty"` // "5m", "15m", "1h", "1d"
	Metric      string    `json:"metric,omitempty"`   // field to extract
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`
}

// DeployMarker records when a new commit was first seen.
type DeployMarker struct {
	bun.BaseModel `bun:"table:deploy_markers" json:"-"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	Service       string    `bun:"service" json:"service"`
	Environment   string    `bun:"environment" json:"environment,omitempty"`
	CommitHash    string    `bun:"commit_hash" json:"commit_hash"`
	FirstSeenAt   time.Time `bun:"first_seen_at" json:"first_seen_at"`
	RequestCount  int       `bun:"request_count" json:"request_count"`
}

// EndpointStat holds aggregated stats for a single endpoint in a time period.
type EndpointStat struct {
	bun.BaseModel    `bun:"table:endpoint_stats" json:"-"`
	ID               int64     `bun:"id,pk,autoincrement" json:"id"`
	Period           string    `bun:"period" json:"period"`
	PeriodStart      time.Time `bun:"period_start" json:"period_start"`
	Service          string    `bun:"service" json:"service,omitempty"`
	Method           string    `bun:"method" json:"method,omitempty"`
	Controller       string    `bun:"controller" json:"controller,omitempty"`
	Action           string    `bun:"action" json:"action,omitempty"`
	PathPattern      string    `bun:"path_pattern" json:"path_pattern,omitempty"`
	RequestCount     int       `bun:"request_count" json:"request_count"`
	ErrorCount       int       `bun:"error_count" json:"error_count"`
	ClientErrorCount int       `bun:"client_error_count" json:"client_error_count"`
	AvgDurationMs    float64   `bun:"avg_duration_ms" json:"avg_duration_ms"`
	P95DurationMs    float64   `bun:"p95_duration_ms" json:"p95_duration_ms"`
	MaxDurationMs    float64   `bun:"max_duration_ms" json:"max_duration_ms"`
	AvgSQLCount      float64   `bun:"avg_sql_count" json:"avg_sql_count"`
	Status2xx        int       `bun:"status_2xx" json:"status_2xx"`
	Status3xx        int       `bun:"status_3xx" json:"status_3xx"`
	Status4xx        int       `bun:"status_4xx" json:"status_4xx"`
	Status5xx        int       `bun:"status_5xx" json:"status_5xx"`
	CreatedAt        time.Time `bun:"created_at" json:"created_at"`
}

// TopEndpointParams defines filters for ranking endpoints.
type TopEndpointParams struct {
	Service     string    `json:"service,omitempty"`
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`
	SortBy      string    `json:"sort_by,omitempty"` // "request_count", "error_rate", "avg_duration", "p95_duration"
	Limit       int       `json:"limit,omitempty"`
	MinRequests int       `json:"min_requests,omitempty"`
}

// HeatmapCell holds one cell of the 24x7 traffic heatmap.
type HeatmapCell struct {
	bun.BaseModel `bun:"table:traffic_heatmap" json:"-"`
	Service       string  `bun:"service" json:"service,omitempty"`
	DayOfWeek     int     `bun:"day_of_week" json:"day_of_week"` // 0=Sunday
	HourOfDay     int     `bun:"hour_of_day" json:"hour_of_day"` // 0-23
	RequestCount  int     `bun:"request_count" json:"request_count"`
	ErrorCount    int     `bun:"error_count" json:"error_count"`
	AvgDurationMs float64 `bun:"avg_duration_ms" json:"avg_duration_ms"`
}

// TrafficSummary holds high-level traffic overview.
type TrafficSummary struct {
	TotalRequests   int            `json:"total_requests"`
	UniqueEndpoints int            `json:"unique_endpoints"`
	ErrorRate       float64        `json:"error_rate"`
	AvgDurationMs   float64        `json:"avg_duration_ms"`
	P95DurationMs   float64        `json:"p95_duration_ms"`
	StatusBreakdown map[string]int `json:"status_breakdown"`
	MethodBreakdown map[string]int `json:"method_breakdown"`
}

// AnalyticsParams defines common filters for analytics queries.
type AnalyticsParams struct {
	Service string    `json:"service,omitempty"`
	Since   time.Time `json:"since"`
	Until   time.Time `json:"until"`
}
