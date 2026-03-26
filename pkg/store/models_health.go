package store

import (
	"time"

	"github.com/uptrace/bun"
)

// HealthCheckStatus represents the current state of a health check target.
type HealthCheckStatus string

const (
	HealthCheckUp       HealthCheckStatus = "up"
	HealthCheckDown     HealthCheckStatus = "down"
	HealthCheckDegraded HealthCheckStatus = "degraded"
)

// HealthCheck represents a configured HTTP endpoint monitor.
type HealthCheck struct {
	bun.BaseModel  `bun:"table:healthchecks" json:"-"`
	ID             string    `bun:"id,pk" json:"id"`
	Name           string    `bun:"name" json:"name"`
	URL            string    `bun:"url" json:"url"`
	Method         string    `bun:"method" json:"method"`
	IntervalSecs   int       `bun:"interval_secs" json:"interval_secs"`
	TimeoutSecs    int       `bun:"timeout_secs" json:"timeout_secs"`
	ExpectedStatus int       `bun:"expected_status" json:"expected_status"`
	ExpectedBody   string    `bun:"expected_body" json:"expected_body,omitempty"`
	Retries        int       `bun:"retries" json:"retries,omitempty"`
	Enabled        bool      `bun:"enabled" json:"enabled"`
	CreatedAt      time.Time `bun:"created_at" json:"created_at"`
}

// HealthCheckResult records a single probe result.
type HealthCheckResult struct {
	bun.BaseModel `bun:"table:healthcheck_results" json:"-"`
	ID            int64             `bun:"id,pk,autoincrement" json:"id"`
	HealthCheckID string            `bun:"healthcheck_id" json:"healthcheck_id"`
	Status        HealthCheckStatus `bun:"status" json:"status"`
	StatusCode    *int              `bun:"status_code" json:"status_code,omitempty"`
	ResponseMs    *int              `bun:"response_ms" json:"response_ms,omitempty"`
	Error         string            `bun:"error" json:"error,omitempty"`
	CheckedAt     time.Time         `bun:"checked_at" json:"checked_at"`
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
