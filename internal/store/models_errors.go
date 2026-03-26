package store

import "time"

// ErrorGroupStatus represents the lifecycle state of an error group.
type ErrorGroupStatus string

const (
	ErrorGroupUnresolved ErrorGroupStatus = "unresolved"
	ErrorGroupResolved   ErrorGroupStatus = "resolved"
	ErrorGroupIgnored    ErrorGroupStatus = "ignored"
)

// ErrorGroup aggregates errors by fingerprint.
type ErrorGroup struct {
	Fingerprint     string            `json:"fingerprint"`
	Service         string            `json:"service"`
	Environment     string            `json:"environment"`
	ExceptionClass  string            `json:"exception_class"`
	Message         string            `json:"message"`
	SourceFile      string            `json:"source_file"`
	SourceLine      int               `json:"source_line"`
	Status          ErrorGroupStatus  `json:"status"`
	FirstSeenAt     time.Time         `json:"first_seen_at"`
	LastSeenAt      time.Time         `json:"last_seen_at"`
	OccurrenceCount int               `json:"occurrence_count"`
	LastLogID       *int64            `json:"last_log_id,omitempty"`
	ReopenedCount   int               `json:"reopened_count"`
	ResolvedAt      *time.Time        `json:"resolved_at,omitempty"`
	IgnoredAt       *time.Time        `json:"ignored_at,omitempty"`
	Events          []ErrorGroupEvent `json:"events,omitempty"`

	// Phase 3: Impact tracking
	UniqueUsers   int            `json:"unique_users"`
	ImpactScore   float64        `json:"impact_score"`
	CommonContext map[string]any `json:"common_context,omitempty"`
}

// ErrorGroupEvent records a lifecycle action on an error group.
type ErrorGroupEvent struct {
	ID          int64     `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Action      string    `json:"action"` // "resolved", "ignored", "reopened"
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListErrorGroupParams defines filters for listing error groups.
type ListErrorGroupParams struct {
	Status      ErrorGroupStatus `json:"status,omitempty"`
	Service     string           `json:"service,omitempty"`
	Environment string           `json:"environment,omitempty"`
	SortBy      string           `json:"sort_by,omitempty"` // "occurrence_count", "last_seen_at", "first_seen_at"
	Limit       int              `json:"limit,omitempty"`
	Offset      int              `json:"offset,omitempty"`
}

// ErrorImpact summarizes the user impact of an error group.
type ErrorImpact struct {
	Fingerprint      string         `json:"fingerprint"`
	UniqueUsers      int            `json:"unique_users"`
	TotalOccurrences int            `json:"total_occurrences"`
	ImpactScore      float64        `json:"impact_score"`
	CommonTraits     map[string]any `json:"common_traits,omitempty"`
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
