package store

import "time"

// ToolTransition tracks how often tool A is followed by tool B.
type ToolTransition struct {
	FromTool       string    `json:"from_tool"`
	ToTool         string    `json:"to_tool"`
	Intent         string    `json:"intent"`
	TotalCount     int       `json:"total_count"`
	ResolvedCount  int       `json:"resolved_count"`
	AbandonedCount int       `json:"abandoned_count"`
	AvgDurationMs  int       `json:"avg_duration_ms"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

// WorkflowTemplate defines a curated or learned workflow step.
type WorkflowTemplate struct {
	ID                   int       `json:"id"`
	Intent               string    `json:"intent"`
	Name                 string    `json:"name"`
	StepOrder            int       `json:"step_order"`
	ToolName             string    `json:"tool_name"`
	ArgsHint             string    `json:"args_hint"`
	Source               string    `json:"source"` // "curated" or "learned"
	ResolvedSessionCount int       `json:"resolved_session_count"`
	CreatedAt            time.Time `json:"created_at"`
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
