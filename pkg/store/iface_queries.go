package store

import (
	"context"
	"time"
)

// QueryMemoryStore manages historical explain_query findings across sessions.
type QueryMemoryStore interface {
	Get(ctx context.Context, fingerprint string) (*QueryMemory, error)
	Upsert(ctx context.Context, params UpsertQueryMemoryParams) error
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// RunbookEffectivenessStore tracks playbook resolution rates.
type RunbookEffectivenessStore interface {
	RecordExecution(ctx context.Context, runbookName string) error
	UpdateOutcome(ctx context.Context, params UpdateRunbookEffectivenessParams) error
	GetMostEffective(ctx context.Context) (*RunbookEffectiveness, error)
	List(ctx context.Context) ([]RunbookEffectiveness, error)
}
