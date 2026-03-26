package store

import (
	"context"
	"time"
)

// EventStore manages generic CI/CD and integration events (Stage 6).
type EventStore interface {
	Create(ctx context.Context, params CreateEventParams) (*Event, error)
	GetByID(ctx context.Context, id int64) (*Event, error)
	List(ctx context.Context, params ListEventParams) ([]Event, error)
	GetByExternalID(ctx context.Context, eventType EventType, externalID string) (*Event, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// TestCorrelationStore manages uncovered error path analysis (Stage 6).
type TestCorrelationStore interface {
	RefreshUncoveredPaths(ctx context.Context) error
	TopByPriority(ctx context.Context, service string, limit int) ([]UncoveredErrorPath, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*UncoveredErrorPath, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
