package store

import (
	"context"
	"time"
)

// MCPActivityStore tracks MCP tool calls and connection events.
type MCPActivityStore interface {
	Log(ctx context.Context, params LogMCPActivityParams) error
	Stats(ctx context.Context) (*MCPActivityStats, error)
	Recent(ctx context.Context, limit int) ([]MCPActivityEvent, error)
	ListByInvestigationSession(ctx context.Context, sessionID string) ([]MCPActivityEvent, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
	SetSuggestionTracking(ctx context.Context, invSessionID string, stepIndex int, wasSuggested bool, rank int) error
	UpdateFollowedBy(ctx context.Context, invSessionID string, stepIndex int, followedBy string) error
}

// AuditStore tracks admin actions for security audit trail.
type AuditStore interface {
	Log(ctx context.Context, params LogAuditParams) error
	Recent(ctx context.Context, limit int) ([]AuditEntry, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
