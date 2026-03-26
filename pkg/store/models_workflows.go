package store

import (
	"time"

	"github.com/uptrace/bun"
)

// ToolTransition tracks how often tool A is followed by tool B.
type ToolTransition struct {
	bun.BaseModel  `bun:"table:tool_transitions" json:"-"`
	FromTool       string    `bun:"from_tool,pk" json:"from_tool"`
	ToTool         string    `bun:"to_tool,pk" json:"to_tool"`
	Intent         string    `bun:"intent,pk" json:"intent"`
	TotalCount     int       `bun:"total_count" json:"total_count"`
	ResolvedCount  int       `bun:"resolved_count" json:"resolved_count"`
	AbandonedCount int       `bun:"abandoned_count" json:"abandoned_count"`
	AvgDurationMs  int       `bun:"avg_duration_ms" json:"avg_duration_ms"`
	LastSeenAt     time.Time `bun:"last_seen_at" json:"last_seen_at"`
}

// WorkflowTemplate defines a curated or learned workflow step.
type WorkflowTemplate struct {
	bun.BaseModel        `bun:"table:workflow_templates" json:"-"`
	ID                   int       `bun:"id,pk,autoincrement" json:"id"`
	Intent               string    `bun:"intent" json:"intent"`
	Name                 string    `bun:"name" json:"name"`
	StepOrder            int       `bun:"step_order" json:"step_order"`
	ToolName             string    `bun:"tool_name" json:"tool_name"`
	ArgsHint             string    `bun:"args_hint" json:"args_hint"`
	Source               string    `bun:"source" json:"source"` // "curated" or "learned"
	ResolvedSessionCount int       `bun:"resolved_session_count" json:"resolved_session_count"`
	CreatedAt            time.Time `bun:"created_at" json:"created_at"`
}

// FindSimilarParams defines criteria for finding similar past sessions.
type FindSimilarParams struct {
	Service          string
	Intent           string
	ToolFingerprint  string
	ExcludeSessionID string
	MaxResults       int
	MinSteps         int
	OnlyResolved     bool
}

// GetTransitionsParams defines criteria for querying tool transitions.
type GetTransitionsParams struct {
	FromTool   string
	Intent     string
	MinSupport int // minimum total_count
	MaxAgeDays int // exclude older than N days
}
