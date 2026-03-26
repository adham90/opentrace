package store

import (
	"time"

	"github.com/uptrace/bun"
)

// QueryMemory stores historical explain_query findings.
type QueryMemory struct {
	bun.BaseModel                  `bun:"table:query_memory" json:"-"`
	Fingerprint                string    `bun:"fingerprint,pk" json:"fingerprint"`
	LastInvestigationSessionID string    `bun:"last_investigation_session_id" json:"last_investigation_session_id"`
	InvestigationCount         int       `bun:"investigation_count" json:"investigation_count"`
	LastRootCause              string    `bun:"last_root_cause" json:"last_root_cause"`
	LastFix                    string    `bun:"last_fix" json:"last_fix"`
	AvgDurationBeforeMs        *int      `bun:"avg_duration_before_ms" json:"avg_duration_before_ms,omitempty"`
	AvgDurationAfterMs         *int      `bun:"avg_duration_after_ms" json:"avg_duration_after_ms,omitempty"`
	FirstSeenAt                time.Time `bun:"first_seen_at" json:"first_seen_at"`
	LastSeenAt                 time.Time `bun:"last_seen_at" json:"last_seen_at"`
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
	bun.BaseModel             `bun:"table:runbook_effectiveness" json:"-"`
	RunbookName               string    `bun:"runbook_name,pk" json:"runbook_name"`
	TotalExecutions           int       `bun:"total_executions" json:"total_executions"`
	ResolvedSessions          int       `bun:"resolved_sessions" json:"resolved_sessions"`
	AbandonedSessions         int       `bun:"abandoned_sessions" json:"abandoned_sessions"`
	AvgStepsAfter             int       `bun:"avg_steps_after" json:"avg_steps_after"`
	AvgSessionDurationSeconds int       `bun:"avg_session_duration_seconds" json:"avg_session_duration_seconds"`
	LastExecutedAt            time.Time `bun:"last_executed_at" json:"last_executed_at"`
}

// UpdateRunbookEffectivenessParams defines input for updating runbook effectiveness.
type UpdateRunbookEffectivenessParams struct {
	RunbookName string
	Outcome     string // "resolved" or "abandoned"
	StepsAfter  int
	DurationSec int
}
