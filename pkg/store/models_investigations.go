package store

import "time"

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
	ID string `json:"id"`

	// Identity
	UserID    string `json:"user_id"`
	UserEmail string `json:"user_email"`
	UserRole  string `json:"user_role"`

	// Client
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version"`
	Workspace     string `json:"workspace"`
	Transport     string `json:"transport"`
	ConnectionID  string `json:"connection_id"`

	// Classification
	Intent              string `json:"intent"`
	IntentDetail        string `json:"intent_detail"`
	PrimaryService      string `json:"primary_service"`
	PrimaryDatasourceID *int   `json:"primary_datasource_id,omitempty"`

	// Outcome
	Status         InvestigationSessionStatus `json:"status"`
	Summary        string                     `json:"summary"`
	RootCause      string                     `json:"root_cause"`
	FixDescription string                     `json:"fix_description"`

	// Metrics
	TotalSteps      int      `json:"total_steps"`
	TotalErrors     int      `json:"total_errors"`
	ToolSequence    []string `json:"tool_sequence"`
	ToolFingerprint string   `json:"tool_fingerprint"`
	ArgSignature    string   `json:"arg_signature"`

	// Timing
	StartedAt       time.Time  `json:"started_at"`
	LastActivityAt  time.Time  `json:"last_activity_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	DurationSeconds int        `json:"duration_seconds"`

	// Subsystem Links
	CreatedWatcherIDs             []string `json:"created_watcher_ids,omitempty"`
	TriggeredByWatcherID          *string  `json:"triggered_by_watcher_id,omitempty"`
	ResolvedErrorGroupIDs         []string `json:"resolved_error_group_ids,omitempty"`
	InvestigatedErrorFingerprints []string `json:"investigated_error_fingerprints,omitempty"`
	CreatedHealthcheckIDs         []string `json:"created_healthcheck_ids,omitempty"`
	TriggeredByHealthcheckID      *string  `json:"triggered_by_healthcheck_id,omitempty"`

	// Stage 4: Additional Subsystem Links
	CreatedNoteIDs            []string           `json:"created_note_ids,omitempty"`
	AutoNoteIDs               []string           `json:"auto_note_ids,omitempty"`
	RunbooksExecuted          []string           `json:"runbooks_executed,omitempty"`
	ExplainedQueries          []string           `json:"explained_queries,omitempty"`
	KilledQueries             []string           `json:"killed_queries,omitempty"`
	TraceIDs                  []string           `json:"trace_ids,omitempty"`
	CorrelatedDeploy          string             `json:"correlated_deploy,omitempty"`
	PreInvestigationSnapshot  map[string]float64 `json:"pre_investigation_snapshot,omitempty"`
	PostInvestigationSnapshot map[string]float64 `json:"post_investigation_snapshot,omitempty"`
	TriggeredByAlertID        *string            `json:"triggered_by_alert_id,omitempty"`

	// Recurrence
	RecurrenceGroup      *string `json:"recurrence_group,omitempty"`
	RecurrenceCount      int     `json:"recurrence_count"`
	PreviousSessionID    *string `json:"previous_session_id,omitempty"`
	FixDurabilitySeconds *int    `json:"fix_durability_seconds,omitempty"`

	// Stage 6: Development session tracking
	FilesModified  []string `json:"files_modified,omitempty"`
	FilesRead      []string `json:"files_read,omitempty"`
	LinkedDeployID *int64   `json:"linked_deploy_id,omitempty"`
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
