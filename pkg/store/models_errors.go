package store

import (
	"time"

	"github.com/uptrace/bun"
)

// ErrorGroupStatus represents the lifecycle state of an error group.
type ErrorGroupStatus string

const (
	ErrorGroupUnresolved ErrorGroupStatus = "unresolved"
	ErrorGroupResolved   ErrorGroupStatus = "resolved"
	ErrorGroupIgnored    ErrorGroupStatus = "ignored"
)

// ErrorGroup aggregates errors by fingerprint.
type ErrorGroup struct {
	bun.BaseModel   `bun:"table:error_groups" json:"-"`
	Fingerprint     string            `bun:"fingerprint,pk" json:"fingerprint"`
	Service         string            `bun:"service" json:"service"`
	Environment     string            `bun:"environment" json:"environment"`
	ExceptionClass  string            `bun:"exception_class" json:"exception_class"`
	Message         string            `bun:"message" json:"message"`
	SourceFile      string            `bun:"source_file" json:"source_file"`
	SourceLine      int               `bun:"source_line" json:"source_line"`
	Status          ErrorGroupStatus  `bun:"status" json:"status"`
	FirstSeenAt     time.Time         `bun:"first_seen_at" json:"first_seen_at"`
	LastSeenAt      time.Time         `bun:"last_seen_at" json:"last_seen_at"`
	OccurrenceCount int               `bun:"occurrence_count" json:"occurrence_count"`
	LastLogID       *int64            `bun:"last_log_id" json:"last_log_id,omitempty"`
	ReopenedCount   int               `bun:"reopened_count" json:"reopened_count"`
	ResolvedAt      *time.Time        `bun:"resolved_at" json:"resolved_at,omitempty"`
	IgnoredAt       *time.Time        `bun:"ignored_at" json:"ignored_at,omitempty"`
	Events          []ErrorGroupEvent `bun:"rel:has-many,join:fingerprint=fingerprint" json:"events,omitempty"`

	// Phase 3: Impact tracking
	UniqueUsers   int            `bun:"unique_users" json:"unique_users"`
	ImpactScore   float64        `bun:"impact_score" json:"impact_score"`
	CommonContext map[string]any `bun:"common_context" json:"common_context,omitempty"`
}

// ErrorGroupEvent records a lifecycle action on an error group.
type ErrorGroupEvent struct {
	bun.BaseModel `bun:"table:error_group_events" json:"-"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	Fingerprint   string    `bun:"fingerprint" json:"fingerprint"`
	Action        string    `bun:"action" json:"action"` // "resolved", "ignored", "reopened"
	Reason        string    `bun:"reason" json:"reason,omitempty"`
	CreatedAt     time.Time `bun:"created_at" json:"created_at"`
}

// ListErrorGroupParams defines filters for listing error groups.
type ListErrorGroupParams struct {
	Status      ErrorGroupStatus `json:"status,omitempty"`
	Service     string           `json:"service,omitempty"`
	Environment string           `json:"environment,omitempty"`
	Since       *time.Time       `json:"since,omitempty"`       // only groups first seen after this time
	SortBy      string           `json:"sort_by,omitempty"`     // "occurrence_count", "last_seen_at", "first_seen_at"
	Limit       int              `json:"limit,omitempty"`
	Offset      int              `json:"offset,omitempty"`
}

// ErrorImpact summarizes the user impact of an error group.
type ErrorImpact struct {
	bun.BaseModel    `bun:"table:error_impacts" json:"-"`
	Fingerprint      string         `bun:"error_fingerprint,pk" json:"fingerprint"`
	UniqueUsers      int            `bun:"-" json:"unique_users"`
	TotalOccurrences int            `bun:"-" json:"total_occurrences"`
	ImpactScore      float64        `bun:"-" json:"impact_score"`
	CommonTraits     map[string]any `bun:"-" json:"common_traits,omitempty"`
}

// AffectedUser represents a user affected by an error.
type AffectedUser struct {
	UserID          string         `json:"user_id"`
	OccurrenceCount int            `json:"occurrence_count"`
	FirstSeenAt     time.Time      `json:"first_seen_at"`
	LastSeenAt      time.Time      `json:"last_seen_at"`
	LastContext     map[string]any `json:"last_context,omitempty"`
}

// ErrorSummary is a lightweight error record for user-scoped queries.
type ErrorSummary struct {
	Fingerprint     string           `json:"fingerprint"`
	ExceptionClass  string           `json:"exception_class"`
	Message         string           `json:"message"`
	OccurrenceCount int              `json:"occurrence_count"`
	FirstSeenAt     time.Time        `json:"first_seen_at"`
	LastSeenAt      time.Time        `json:"last_seen_at"`
	Status          ErrorGroupStatus `json:"status"`
}

// ImpactQueryParams defines filters for querying errors by impact.
type ImpactQueryParams struct {
	Status  ErrorGroupStatus `json:"status,omitempty"`
	Service string           `json:"service,omitempty"`
	Since   time.Time        `json:"since"`
	SortBy  string           `json:"sort_by,omitempty"` // impact_score, unique_users, occurrence_count, last_seen
	Limit   int              `json:"limit,omitempty"`
}

// ErrorGroupWithImpact extends ErrorGroup with impact details.
type ErrorGroupWithImpact struct {
	ErrorGroup
	TopAffectedUsers []AffectedUser `json:"top_affected_users,omitempty"`
}
