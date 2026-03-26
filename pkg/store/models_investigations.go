package store

import (
	"time"

	"github.com/uptrace/bun"
)

// InvestigationSessionStatus represents the lifecycle state of an investigation session.
type InvestigationSessionStatus string

const (
	InvestigationStatusOpen       InvestigationSessionStatus = "open"
	InvestigationStatusResolved   InvestigationSessionStatus = "resolved"
	InvestigationStatusUnresolved InvestigationSessionStatus = "unresolved"
	InvestigationStatusAbandoned  InvestigationSessionStatus = "abandoned"
)

// InvestigationSession represents a complete MCP investigation session.
type InvestigationSession struct {
	bun.BaseModel `bun:"table:investigation_sessions" json:"-"`
	ID            string `bun:"id,pk" json:"id"`

	// Identity
	UserID    string `bun:"user_id" json:"user_id"`
	UserEmail string `bun:"user_email" json:"user_email"`
	UserRole  string `bun:"user_role" json:"user_role"`

	// Client
	ClientName    string `bun:"client_name" json:"client_name"`
	ClientVersion string `bun:"client_version" json:"client_version"`
	Workspace     string `bun:"workspace" json:"workspace"`
	Transport     string `bun:"transport" json:"transport"`
	ConnectionID  string `bun:"connection_id" json:"connection_id"`

	// Classification
	Intent              string `bun:"intent" json:"intent"`
	IntentDetail        string `bun:"intent_detail" json:"intent_detail"`
	PrimaryService      string `bun:"primary_service" json:"primary_service"`
	PrimaryDatasourceID *int   `bun:"primary_datasource_id" json:"primary_datasource_id,omitempty"`

	// Outcome
	Status         InvestigationSessionStatus `bun:"status" json:"status"`
	Summary        string                     `bun:"summary" json:"summary"`
	RootCause      string                     `bun:"root_cause" json:"root_cause"`
	FixDescription string                     `bun:"fix_description" json:"fix_description"`

	// Metrics
	TotalSteps      int      `bun:"total_steps" json:"total_steps"`
	TotalErrors     int      `bun:"total_errors" json:"total_errors"`
	ToolSequence    []string `bun:"tool_sequence" json:"tool_sequence"`
	ToolFingerprint string   `bun:"tool_fingerprint" json:"tool_fingerprint"`
	ArgSignature    string   `bun:"arg_signature" json:"arg_signature"`

	// Timing
	StartedAt       time.Time  `bun:"started_at" json:"started_at"`
	LastActivityAt  time.Time  `bun:"last_activity_at" json:"last_activity_at"`
	EndedAt         *time.Time `bun:"ended_at" json:"ended_at,omitempty"`
	DurationSeconds int        `bun:"duration_seconds" json:"duration_seconds"`

	// Subsystem Links
	CreatedWatcherIDs             []string `bun:"created_watcher_ids" json:"created_watcher_ids,omitempty"`
	TriggeredByWatcherID          *string  `bun:"triggered_by_watcher_id" json:"triggered_by_watcher_id,omitempty"`
	ResolvedErrorGroupIDs         []string `bun:"resolved_error_group_ids" json:"resolved_error_group_ids,omitempty"`
	InvestigatedErrorFingerprints []string `bun:"investigated_error_fingerprints" json:"investigated_error_fingerprints,omitempty"`
	CreatedHealthcheckIDs         []string `bun:"created_healthcheck_ids" json:"created_healthcheck_ids,omitempty"`
	TriggeredByHealthcheckID      *string  `bun:"triggered_by_healthcheck_id" json:"triggered_by_healthcheck_id,omitempty"`

	// Stage 4: Additional Subsystem Links
	CreatedNoteIDs            []string           `bun:"created_note_ids" json:"created_note_ids,omitempty"`
	AutoNoteIDs               []string           `bun:"auto_note_ids" json:"auto_note_ids,omitempty"`
	RunbooksExecuted          []string           `bun:"runbooks_executed" json:"runbooks_executed,omitempty"`
	ExplainedQueries          []string           `bun:"explained_queries" json:"explained_queries,omitempty"`
	KilledQueries             []string           `bun:"killed_queries" json:"killed_queries,omitempty"`
	TraceIDs                  []string           `bun:"trace_ids" json:"trace_ids,omitempty"`
	CorrelatedDeploy          string             `bun:"correlated_deploy" json:"correlated_deploy,omitempty"`
	PreInvestigationSnapshot  map[string]float64 `bun:"pre_investigation_snapshot" json:"pre_investigation_snapshot,omitempty"`
	PostInvestigationSnapshot map[string]float64 `bun:"post_investigation_snapshot" json:"post_investigation_snapshot,omitempty"`
	TriggeredByAlertID        *string            `bun:"triggered_by_alert_id" json:"triggered_by_alert_id,omitempty"`

	// Recurrence
	RecurrenceGroup      *string `bun:"recurrence_group" json:"recurrence_group,omitempty"`
	RecurrenceCount      int     `bun:"recurrence_count" json:"recurrence_count"`
	PreviousSessionID    *string `bun:"previous_session_id" json:"previous_session_id,omitempty"`
	FixDurabilitySeconds *int    `bun:"fix_durability_seconds" json:"fix_durability_seconds,omitempty"`

	// Stage 6: Development session tracking
	FilesModified  []string `bun:"files_modified" json:"files_modified,omitempty"`
	FilesRead      []string `bun:"files_read" json:"files_read,omitempty"`
	LinkedDeployID *int64   `bun:"linked_deploy_id" json:"linked_deploy_id,omitempty"`
}

// CreateInvestigationSessionParams defines input for creating an investigation session.
type CreateInvestigationSessionParams struct {
	UserID        string `json:"user_id"`
	UserEmail     string `json:"user_email"`
	UserRole      string `json:"user_role"`
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version"`
	Workspace     string `json:"workspace"`
	Transport     string `json:"transport"`
	ConnectionID  string `json:"connection_id"`
}

// UpdateInvestigationSessionParams defines fields that can be updated on a session.
type UpdateInvestigationSessionParams struct {
	Intent              *string                     `json:"intent,omitempty"`
	IntentDetail        *string                     `json:"intent_detail,omitempty"`
	PrimaryService      *string                     `json:"primary_service,omitempty"`
	PrimaryDatasourceID *int                        `json:"primary_datasource_id,omitempty"`
	Status              *InvestigationSessionStatus `json:"status,omitempty"`
	Summary             *string                     `json:"summary,omitempty"`
	RootCause           *string                     `json:"root_cause,omitempty"`
	FixDescription      *string                     `json:"fix_description,omitempty"`
	TotalSteps          *int                        `json:"total_steps,omitempty"`
	TotalErrors         *int                        `json:"total_errors,omitempty"`
	ToolSequence        []string                    `json:"tool_sequence,omitempty"`
	ToolFingerprint     *string                     `json:"tool_fingerprint,omitempty"`
	ArgSignature        *string                     `json:"arg_signature,omitempty"`

	// Subsystem links
	CreatedWatcherIDs             []string `json:"created_watcher_ids,omitempty"`
	TriggeredByWatcherID          *string  `json:"triggered_by_watcher_id,omitempty"`
	ResolvedErrorGroupIDs         []string `json:"resolved_error_group_ids,omitempty"`
	InvestigatedErrorFingerprints []string `json:"investigated_error_fingerprints,omitempty"`
	CreatedHealthcheckIDs         []string `json:"created_healthcheck_ids,omitempty"`
	TriggeredByHealthcheckID      *string  `json:"triggered_by_healthcheck_id,omitempty"`

	// Stage 4: Additional Subsystem Links
	RunbooksExecuted          []string `json:"runbooks_executed,omitempty"`
	ExplainedQueries          []string `json:"explained_queries,omitempty"`
	KilledQueries             []string `json:"killed_queries,omitempty"`
	TraceIDs                  []string `json:"trace_ids,omitempty"`
	CorrelatedDeploy          *string  `json:"correlated_deploy,omitempty"`
	PreInvestigationSnapshot  *string  `json:"pre_investigation_snapshot,omitempty"`  // JSON string
	PostInvestigationSnapshot *string  `json:"post_investigation_snapshot,omitempty"` // JSON string
	AutoNoteIDs               []string `json:"auto_note_ids,omitempty"`
	CreatedNoteIDs            []string `json:"created_note_ids,omitempty"`

	// Recurrence
	RecurrenceGroup      *string `json:"recurrence_group,omitempty"`
	RecurrenceCount      *int    `json:"recurrence_count,omitempty"`
	PreviousSessionID    *string `json:"previous_session_id,omitempty"`
	FixDurabilitySeconds *int    `json:"fix_durability_seconds,omitempty"`

	// Stage 6: Development session tracking
	FilesModified  []string `json:"files_modified,omitempty"`
	FilesRead      []string `json:"files_read,omitempty"`
	LinkedDeployID *int64   `json:"linked_deploy_id,omitempty"`
}

// FindRecentSessionParams defines criteria for finding a recent resumable session.
type FindRecentSessionParams struct {
	UserID    string
	Workspace string
	MaxAge    time.Duration
	Status    InvestigationSessionStatus
}

// ListInvestigationSessionParams defines filters for listing investigation sessions.
type ListInvestigationSessionParams struct {
	UserID  string                     `json:"user_id,omitempty"`
	Status  InvestigationSessionStatus `json:"status,omitempty"`
	Intent  string                     `json:"intent,omitempty"`
	Service string                     `json:"service,omitempty"`
	Since   time.Time                  `json:"since"`
	Limit   int                        `json:"limit,omitempty"`
	Offset  int                        `json:"offset,omitempty"`
}

// InvestigationSessionStats holds aggregated investigation session statistics.
type InvestigationSessionStats struct {
	TotalSessions    int     `json:"total_sessions"`
	OpenSessions     int     `json:"open_sessions"`
	ResolvedSessions int     `json:"resolved_sessions"`
	AvgSteps         float64 `json:"avg_steps"`
	AvgDurationSecs  float64 `json:"avg_duration_secs"`
}
