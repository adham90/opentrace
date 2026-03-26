package store

import (
	"context"
	"time"
)

// AgentNoteStore manages persistent notes for the AI agent.
type AgentNoteStore interface {
	Upsert(ctx context.Context, entityType, entityID, note string) (*AgentNote, error)
	Get(ctx context.Context, entityType, entityID string) (*AgentNote, error)
	List(ctx context.Context, entityType string) ([]AgentNote, error)
	Delete(ctx context.Context, entityType, entityID string) error
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
