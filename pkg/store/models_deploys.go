package store

import "time"

// DeployStatus represents the lifecycle state of a deploy.
type DeployStatus string

const (
	DeployStatusPending  DeployStatus = "pending"
	DeployStatusMeasured DeployStatus = "measured"
	DeployStatusIncident DeployStatus = "incident"
)

// DeploySource identifies how the deploy was recorded.
type DeploySource string

const (
	DeploySourceWebhook      DeploySource = "webhook"
	DeploySourceAutoDetected DeploySource = "auto-detected"
	DeploySourceManual       DeploySource = "manual"
)

// Deploy represents a recorded deployment event.
type Deploy struct {
	ID                     int64        `json:"id"`
	Service                string       `json:"service"`
	Environment            string       `json:"environment"`
	CommitHash             string       `json:"commit_hash"`
	Branch                 string       `json:"branch"`
	Author                 string       `json:"author"`
	FilesChanged           []string     `json:"files_changed,omitempty"`
	DeploySource           DeploySource `json:"deploy_source"`
	PreErrorRate           *float64     `json:"pre_error_rate,omitempty"`
	PostErrorRate          *float64     `json:"post_error_rate,omitempty"`
	PreAvgDurationMs       *float64     `json:"pre_avg_duration_ms,omitempty"`
	PostAvgDurationMs      *float64     `json:"post_avg_duration_ms,omitempty"`
	ImpactMeasuredAt       *time.Time   `json:"impact_measured_at,omitempty"`
	LinkedInvestigationIDs []string     `json:"linked_investigation_ids,omitempty"`
	Status                 DeployStatus `json:"status"`
	DeployedAt             time.Time    `json:"deployed_at"`
	CreatedAt              time.Time    `json:"created_at"`
}

// CreateDeployParams defines input for recording a new deploy.
type CreateDeployParams struct {
	Service      string
	Environment  string
	CommitHash   string
	Branch       string
	Author       string
	FilesChanged []string
	DeploySource DeploySource
	DeployedAt   *time.Time // nil = now
}

// DeployImpact holds measured before/after metrics for a deploy.
type DeployImpact struct {
	PreErrorRate       float64 `json:"pre_error_rate"`
	PostErrorRate      float64 `json:"post_error_rate"`
	PreAvgDurationMs   float64 `json:"pre_avg_duration_ms"`
	PostAvgDurationMs  float64 `json:"post_avg_duration_ms"`
	ErrorRateChangePct float64 `json:"error_rate_change_pct"`
	DurationChangePct  float64 `json:"duration_change_pct"`
	IsIncident         bool    `json:"is_incident"`
}
