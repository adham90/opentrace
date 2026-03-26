package store

import "time"

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
	ID          int64          `json:"id"`
	EventType   EventType      `json:"event_type"`
	Source      string         `json:"source"`
	Service     string         `json:"service"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	ExternalID  string         `json:"external_id,omitempty"`
	ExternalURL string         `json:"external_url,omitempty"`
	Author      string         `json:"author,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
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
	ID                 int64     `json:"id"`
	Service            string    `json:"service"`
	ErrorFingerprint   string    `json:"error_fingerprint"`
	ErrorClass         string    `json:"error_class"`
	SourceFile         string    `json:"source_file"`
	Endpoint           string    `json:"endpoint"`
	ErrorCount         int       `json:"error_count"`
	UserImpactScore    float64   `json:"user_impact_score"`
	InvestigationCount int       `json:"investigation_count"`
	PriorityScore      float64   `json:"priority_score"`
	LastSeenAt         time.Time `json:"last_seen_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
