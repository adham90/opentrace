package store

import (
	"time"

	"github.com/uptrace/bun"
)

// CodeEntityType identifies the kind of source code entity.
type CodeEntityType string

const (
	CodeEntityFile       CodeEntityType = "file"
	CodeEntityController CodeEntityType = "controller"
	CodeEntityEndpoint   CodeEntityType = "endpoint"
)

// CodeEntity represents a tracked source code path with risk scoring.
type CodeEntity struct {
	bun.BaseModel       `bun:"table:code_entities" json:"-"`
	ID                  int64          `bun:"id,pk,autoincrement" json:"id"`
	EntityType          CodeEntityType `bun:"entity_type" json:"entity_type"`
	EntityName          string         `bun:"entity_name" json:"entity_name"`
	Service             string         `bun:"service" json:"service"`
	RiskScore           float64        `bun:"risk_score" json:"risk_score"`
	ErrorCount          int            `bun:"error_count" json:"error_count"`
	InvestigationCount  int            `bun:"investigation_count" json:"investigation_count"`
	AvgDurationMs       *float64       `bun:"avg_duration_ms" json:"avg_duration_ms,omitempty"`
	LastErrorAt         *time.Time     `bun:"last_error_at" json:"last_error_at,omitempty"`
	LastInvestigationAt *time.Time     `bun:"last_investigation_at" json:"last_investigation_at,omitempty"`
	Metadata            map[string]any `bun:"metadata_json" json:"metadata,omitempty"`
	CreatedAt           time.Time      `bun:"created_at" json:"created_at"`
	UpdatedAt           time.Time      `bun:"updated_at" json:"updated_at"`
}

// UpsertCodeEntityParams defines input for creating or updating a code entity.
type UpsertCodeEntityParams struct {
	EntityType CodeEntityType
	EntityName string
	Service    string
}

// UncoveredErrorPath tracks production error paths that lack test coverage.
type UncoveredErrorPath struct {
	bun.BaseModel      `bun:"table:uncovered_error_paths" json:"-"`
	ID                 int64     `bun:"id,pk,autoincrement" json:"id"`
	Service            string    `bun:"service" json:"service"`
	ErrorFingerprint   string    `bun:"error_fingerprint" json:"error_fingerprint"`
	ErrorClass         string    `bun:"error_class" json:"error_class"`
	SourceFile         string    `bun:"source_file" json:"source_file"`
	Endpoint           string    `bun:"endpoint" json:"endpoint"`
	ErrorCount         int       `bun:"error_count" json:"error_count"`
	UserImpactScore    float64   `bun:"user_impact_score" json:"user_impact_score"`
	InvestigationCount int       `bun:"investigation_count" json:"investigation_count"`
	PriorityScore      float64   `bun:"priority_score" json:"priority_score"`
	LastSeenAt         time.Time `bun:"last_seen_at" json:"last_seen_at"`
	CreatedAt          time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt          time.Time `bun:"updated_at" json:"updated_at"`
}
