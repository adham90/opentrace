package store

import "time"

// MCPActivityEvent represents a single MCP tool call or connection event.
type MCPActivityEvent struct {
	ID            int64     `json:"id"`
	SessionID     string    `json:"session_id"`
	UserID        string    `json:"user_id,omitempty"`
	ToolName      string    `json:"tool_name"`
	Arguments     string    `json:"arguments,omitempty"`
	ResultPreview string    `json:"result_preview,omitempty"`
	IsError       bool      `json:"is_error"`
	DurationMs    *int64    `json:"duration_ms,omitempty"`
	EventType     string    `json:"event_type"`
	CreatedAt     time.Time `json:"created_at"`
}

// LogMCPActivityParams defines input for logging an MCP activity event.
type LogMCPActivityParams struct {
	SessionID              string
	UserID                 string
	ToolName               string
	Arguments              string
	ResultPreview          string
	IsError                bool
	DurationMs             *int64
	EventType              string // "tool_call", "connect", "disconnect"
	InvestigationSessionID string // links to investigation_sessions.id
	StepIndex              int    // step number within the investigation session
	WasSuggested           bool   // whether this tool was in the previous suggested_tools
	SuggestionRank         int    // rank in the suggestion list (0 if not suggested)
	FollowedBy             string // tool name that followed this one (unused in INSERT, kept for compat)
	PreviousStepIndex      int    // step index of the previous tool (for updating followed_by)
}

// MCPActivityStats holds aggregated MCP activity statistics.
type MCPActivityStats struct {
	ActiveSessions int        `json:"active_sessions"`
	CallsLastHour  int        `json:"calls_last_hour"`
	ErrorsLastHour int        `json:"errors_last_hour"`
	LastActivity   *time.Time `json:"last_activity,omitempty"`
}
