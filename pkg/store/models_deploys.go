package store

import (
	"time"

	"github.com/uptrace/bun"
)

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
	bun.BaseModel          `bun:"table:deploys" json:"-"`
	ID                     int64        `bun:"id,pk,autoincrement" json:"id"`
	Service                string       `bun:"service" json:"service"`
	Environment            string       `bun:"environment" json:"environment"`
	CommitHash             string       `bun:"commit_hash" json:"commit_hash"`
	Branch                 string       `bun:"branch" json:"branch"`
	Author                 string       `bun:"author" json:"author"`
	FilesChanged           []string     `bun:"files_changed_json" json:"files_changed,omitempty"`
	DeploySource           DeploySource `bun:"deploy_source" json:"deploy_source"`
	PreErrorRate           *float64     `bun:"pre_error_rate" json:"pre_error_rate,omitempty"`
	PostErrorRate          *float64     `bun:"post_error_rate" json:"post_error_rate,omitempty"`
	PreAvgDurationMs       *float64     `bun:"pre_avg_duration_ms" json:"pre_avg_duration_ms,omitempty"`
	PostAvgDurationMs      *float64     `bun:"post_avg_duration_ms" json:"post_avg_duration_ms,omitempty"`
	ImpactMeasuredAt       *time.Time   `bun:"impact_measured_at" json:"impact_measured_at,omitempty"`
	LinkedInvestigationIDs []string     `bun:"linked_investigation_ids_json" json:"linked_investigation_ids,omitempty"`
	Status                 DeployStatus `bun:"status" json:"status"`
	DeployedAt             time.Time    `bun:"deployed_at" json:"deployed_at"`
	CreatedAt              time.Time    `bun:"created_at" json:"created_at"`
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
