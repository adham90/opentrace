package store

import (
	"time"

	"github.com/uptrace/bun"
)

// MCPActivityEvent represents a single MCP tool call or connection event.
type MCPActivityEvent struct {
	bun.BaseModel `bun:"table:mcp_activity" json:"-"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	SessionID     string    `bun:"session_id" json:"session_id"`
	UserID        string    `bun:"user_id" json:"user_id,omitempty"`
	ToolName      string    `bun:"tool_name" json:"tool_name"`
	Arguments     string    `bun:"arguments" json:"arguments,omitempty"`
	ResultPreview string    `bun:"result_preview" json:"result_preview,omitempty"`
	IsError       bool      `bun:"is_error" json:"is_error"`
	DurationMs    *int64    `bun:"duration_ms" json:"duration_ms,omitempty"`
	EventType     string    `bun:"event_type" json:"event_type"`
	CreatedAt     time.Time `bun:"created_at" json:"created_at"`
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
