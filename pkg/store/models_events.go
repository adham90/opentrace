package store

import (
	"time"

	"github.com/uptrace/bun"
)

// EventType identifies the kind of CI/CD or integration event.
type EventType string

const (
	EventTypeDeploy EventType = "deploy"
	EventTypePR     EventType = "pr"
	EventTypeTest   EventType = "test"
	EventTypeAlert  EventType = "alert"
	EventTypeCommit EventType = "commit"
	EventTypeCustom EventType = "custom"
)

// Event represents a generic CI/CD or integration event.
type Event struct {
	bun.BaseModel `bun:"table:events" json:"-"`
	ID            int64          `bun:"id,pk,autoincrement" json:"id"`
	EventType     EventType      `bun:"event_type" json:"event_type"`
	Source        string         `bun:"source" json:"source"`
	Service       string         `bun:"service" json:"service"`
	Title         string         `bun:"title" json:"title"`
	Description   string         `bun:"description" json:"description"`
	Metadata      map[string]any `bun:"metadata_json" json:"metadata,omitempty"`
	ExternalID    string         `bun:"external_id" json:"external_id,omitempty"`
	ExternalURL   string         `bun:"external_url" json:"external_url,omitempty"`
	Author        string         `bun:"author" json:"author,omitempty"`
	CreatedAt     time.Time      `bun:"created_at" json:"created_at"`
}

// CreateEventParams defines input for creating an event.
type CreateEventParams struct {
	EventType   EventType      `json:"event_type"`
	Source      string         `json:"source"`
	Service     string         `json:"service"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	ExternalID  string         `json:"external_id,omitempty"`
	ExternalURL string         `json:"external_url,omitempty"`
	Author      string         `json:"author,omitempty"`
}

// ListEventParams defines filters for listing events.
type ListEventParams struct {
	EventType EventType `json:"event_type,omitempty"`
	Service   string    `json:"service,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	Limit     int       `json:"limit,omitempty"`
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
