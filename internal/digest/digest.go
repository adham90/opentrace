package digest

import "time"

// DigestStatus represents the overall health status of a digest.
type DigestStatus string

const (
	StatusHealthy        DigestStatus = "healthy"
	StatusInfoOnly       DigestStatus = "info_only"
	StatusNeedsAttention DigestStatus = "needs_attention"
	StatusCritical       DigestStatus = "critical"
	StatusDegraded       DigestStatus = "degraded"
)

// Digest is a structured health summary covering alerts, watchers, and trends
// for a given time period.
type Digest struct {
	ID             string         `json:"id"`
	GeneratedAt    time.Time      `json:"generated_at"`
	PeriodStart    time.Time      `json:"period_start"`
	PeriodEnd      time.Time      `json:"period_end"`
	Environment    string         `json:"environment,omitempty"`
	Status         DigestStatus   `json:"status"`
	AlertSummary   AlertSummary   `json:"alert_summary"`
	WatcherSummary WatcherSummary `json:"watcher_summary"`
	TopAlerts      []AlertItem    `json:"top_alerts"`
	WatcherHealth  []WatcherHealth `json:"watcher_health"`
	Trends         *Trends        `json:"trends,omitempty"`
}

// AlertSummary holds aggregate alert counts for the period.
type AlertSummary struct {
	Total       int `json:"total"`
	Critical    int `json:"critical"`
	Warning     int `json:"warning"`
	Info        int `json:"info"`
	Unread      int `json:"unread"`
	NewInPeriod int `json:"new_in_period"`
}

// WatcherSummary holds aggregate watcher counts.
type WatcherSummary struct {
	Total        int `json:"total"`
	Active       int `json:"active"`
	Paused       int `json:"paused"`
	InError      int `json:"in_error"`
	RunsInPeriod int `json:"runs_in_period"`
	FailedRuns   int `json:"failed_runs"`
}

// AlertItem is a single alert entry in the digest.
type AlertItem struct {
	ID           string    `json:"id"`
	WatcherTitle string    `json:"watcher_title"`
	Severity     string    `json:"severity"`
	Summary      string    `json:"summary"`
	CreatedAt    time.Time `json:"created_at"`
	Read         bool      `json:"read"`
}

// WatcherHealth shows the health of a single watcher during the digest period.
type WatcherHealth struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastRunOK  bool       `json:"last_run_ok"`
	RunCount   int        `json:"run_count"`
	AlertCount int        `json:"alert_count"`
}

// Trends stores raw counts for current and previous periods.
// The presentation layer formats these as percentages.
type Trends struct {
	AlertsPrevCount    int `json:"alerts_prev_count"`
	AlertsCurrentCount int `json:"alerts_current_count"`
	FailedRunsPrev     int `json:"failed_runs_prev"`
	FailedRunsCurrent  int `json:"failed_runs_current"`
}

// AlertsChangePercent returns the percentage change in alerts vs previous period.
// Returns 0 if previous count is 0.
func (t *Trends) AlertsChangePercent() float64 {
	if t.AlertsPrevCount == 0 {
		return 0
	}
	return float64(t.AlertsCurrentCount-t.AlertsPrevCount) / float64(t.AlertsPrevCount) * 100
}

// FailedRunsChangePercent returns the percentage change in failed runs vs previous period.
// Returns 0 if previous count is 0.
func (t *Trends) FailedRunsChangePercent() float64 {
	if t.FailedRunsPrev == 0 {
		return 0
	}
	return float64(t.FailedRunsCurrent-t.FailedRunsPrev) / float64(t.FailedRunsPrev) * 100
}
