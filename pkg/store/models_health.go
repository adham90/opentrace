package store

import "time"

// HealthCheckStatus represents the current state of a health check target.
type HealthCheckStatus string

const (
	HealthCheckUp       HealthCheckStatus = "up"
	HealthCheckDown     HealthCheckStatus = "down"
	HealthCheckDegraded HealthCheckStatus = "degraded"
)

// HealthCheck represents a configured HTTP endpoint monitor.
type HealthCheck struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	Method         string    `json:"method"`
	IntervalSecs   int       `json:"interval_secs"`
	TimeoutSecs    int       `json:"timeout_secs"`
	ExpectedStatus int       `json:"expected_status"`
	ExpectedBody   string    `json:"expected_body,omitempty"`
	Retries        int       `json:"retries,omitempty"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
}

// HealthCheckResult records a single probe result.
type HealthCheckResult struct {
	ID            int64             `json:"id"`
	HealthCheckID string            `json:"healthcheck_id"`
	Status        HealthCheckStatus `json:"status"`
	StatusCode    *int              `json:"status_code,omitempty"`
	ResponseMs    *int              `json:"response_ms,omitempty"`
	Error         string            `json:"error,omitempty"`
	CheckedAt     time.Time         `json:"checked_at"`
}

// CreateHealthCheckParams defines the input for creating a health check.
type CreateHealthCheckParams struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	Method         string `json:"method,omitempty"`
	IntervalSecs   int    `json:"interval_secs,omitempty"`
	TimeoutSecs    int    `json:"timeout_secs,omitempty"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
	ExpectedBody   string `json:"expected_body,omitempty"`
	Retries        int    `json:"retries,omitempty"`
}

// UptimeSummary aggregates uptime stats for a single health check.
type UptimeSummary struct {
	HealthCheckID string  `json:"healthcheck_id"`
	Name          string  `json:"name"`
	URL           string  `json:"url"`
	CurrentStatus string  `json:"current_status"`
	UptimePct     float64 `json:"uptime_pct"`
	AvgResponseMs float64 `json:"avg_response_ms"`
	TotalChecks   int     `json:"total_checks"`
	DownChecks    int     `json:"down_checks"`
	Reliability   float64 `json:"reliability,omitempty"`
}

// ListHealthCheckParams controls pagination for health check listing.
type ListHealthCheckParams struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}
