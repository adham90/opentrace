package store

import "time"

// MetricBucket holds pre-aggregated metrics for a time bucket.
type MetricBucket struct {
	ID                int64     `json:"id"`
	BucketStart       time.Time `json:"bucket_start"`
	BucketInterval    string    `json:"bucket_interval"`
	Service           string    `json:"service,omitempty"`
	Endpoint          string    `json:"endpoint,omitempty"`
	Environment       string    `json:"environment,omitempty"`
	RequestCount      int       `json:"request_count"`
	ErrorCount        int       `json:"error_count"`
	LogCount          int       `json:"log_count"`
	AvgDurationMs     float64   `json:"avg_duration_ms"`
	P50DurationMs     float64   `json:"p50_duration_ms"`
	P95DurationMs     float64   `json:"p95_duration_ms"`
	P99DurationMs     float64   `json:"p99_duration_ms"`
	MaxDurationMs     float64   `json:"max_duration_ms"`
	AvgSQLCount       float64   `json:"avg_sql_count"`
	AvgDBTimeMs       float64   `json:"avg_db_time_ms"`
	AvgCacheHitRatio  float64   `json:"avg_cache_hit_ratio"`
	AvgHTTPExternalMs float64   `json:"avg_http_external_ms"`
	CreatedAt         time.Time `json:"created_at"`
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
	ID           int64     `json:"id"`
	Service      string    `json:"service"`
	Environment  string    `json:"environment,omitempty"`
	CommitHash   string    `json:"commit_hash"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	RequestCount int       `json:"request_count"`
}

// EndpointStat holds aggregated stats for a single endpoint in a time period.
type EndpointStat struct {
	ID               int64     `json:"id"`
	Period           string    `json:"period"`
	PeriodStart      time.Time `json:"period_start"`
	Service          string    `json:"service,omitempty"`
	Method           string    `json:"method,omitempty"`
	Controller       string    `json:"controller,omitempty"`
	Action           string    `json:"action,omitempty"`
	PathPattern      string    `json:"path_pattern,omitempty"`
	RequestCount     int       `json:"request_count"`
	ErrorCount       int       `json:"error_count"`
	ClientErrorCount int       `json:"client_error_count"`
	AvgDurationMs    float64   `json:"avg_duration_ms"`
	P95DurationMs    float64   `json:"p95_duration_ms"`
	MaxDurationMs    float64   `json:"max_duration_ms"`
	AvgSQLCount      float64   `json:"avg_sql_count"`
	Status2xx        int       `json:"status_2xx"`
	Status3xx        int       `json:"status_3xx"`
	Status4xx        int       `json:"status_4xx"`
	Status5xx        int       `json:"status_5xx"`
	CreatedAt        time.Time `json:"created_at"`
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
	Service       string  `json:"service,omitempty"`
	DayOfWeek     int     `json:"day_of_week"` // 0=Sunday
	HourOfDay     int     `json:"hour_of_day"` // 0-23
	RequestCount  int     `json:"request_count"`
	ErrorCount    int     `json:"error_count"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
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
