package store

import "time"

// QueryMemory stores historical explain_query findings.
type QueryMemory struct {
	Fingerprint                string    `json:"fingerprint"`
	LastInvestigationSessionID string    `json:"last_investigation_session_id"`
	InvestigationCount         int       `json:"investigation_count"`
	LastRootCause              string    `json:"last_root_cause"`
	LastFix                    string    `json:"last_fix"`
	AvgDurationBeforeMs        *int      `json:"avg_duration_before_ms,omitempty"`
	AvgDurationAfterMs         *int      `json:"avg_duration_after_ms,omitempty"`
	FirstSeenAt                time.Time `json:"first_seen_at"`
	LastSeenAt                 time.Time `json:"last_seen_at"`
}

// UpsertQueryMemoryParams defines input for creating/updating query memory.
type UpsertQueryMemoryParams struct {
	Fingerprint string
	SessionID   string
	RootCause   string
	Fix         string
	DurationMs  *int
}

// RunbookEffectiveness tracks how well each playbook resolves issues.
type RunbookEffectiveness struct {
	RunbookName               string    `json:"runbook_name"`
	TotalExecutions           int       `json:"total_executions"`
	ResolvedSessions          int       `json:"resolved_sessions"`
	AbandonedSessions         int       `json:"abandoned_sessions"`
	AvgStepsAfter             int       `json:"avg_steps_after"`
	AvgSessionDurationSeconds int       `json:"avg_session_duration_seconds"`
	LastExecutedAt            time.Time `json:"last_executed_at"`
}

// UpdateRunbookEffectivenessParams defines input for updating runbook effectiveness.
type UpdateRunbookEffectivenessParams struct {
	RunbookName string
	Outcome     string // "resolved" or "abandoned"
	StepsAfter  int
	DurationSec int
}
