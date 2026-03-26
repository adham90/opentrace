package store

import "time"

// CodeEntityType identifies the kind of source code entity.
type CodeEntityType string

const (
	CodeEntityFile       CodeEntityType = "file"
	CodeEntityController CodeEntityType = "controller"
	CodeEntityEndpoint   CodeEntityType = "endpoint"
)

// CodeEntity represents a tracked source code path with risk scoring.
type CodeEntity struct {
	ID                  int64          `json:"id"`
	EntityType          CodeEntityType `json:"entity_type"`
	EntityName          string         `json:"entity_name"`
	Service             string         `json:"service"`
	RiskScore           float64        `json:"risk_score"`
	ErrorCount          int            `json:"error_count"`
	InvestigationCount  int            `json:"investigation_count"`
	AvgDurationMs       *float64       `json:"avg_duration_ms,omitempty"`
	LastErrorAt         *time.Time     `json:"last_error_at,omitempty"`
	LastInvestigationAt *time.Time     `json:"last_investigation_at,omitempty"`
	Metadata            map[string]any `json:"metadata,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

// UpsertCodeEntityParams defines input for creating or updating a code entity.
type UpsertCodeEntityParams struct {
	EntityType CodeEntityType
	EntityName string
	Service    string
}
