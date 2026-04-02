package store

import (
	"context"
	"time"
)

// CodeEntityStore manages code entity risk tracking (Stage 5).
type CodeEntityStore interface {
	Upsert(ctx context.Context, params UpsertCodeEntityParams) (*CodeEntity, error)
	GetByName(ctx context.Context, entityType CodeEntityType, entityName, service string) (*CodeEntity, error)
	TopByRisk(ctx context.Context, service string, limit int) ([]CodeEntity, error)
	BatchGetRisk(ctx context.Context, entityType CodeEntityType, names []string, service string) ([]CodeEntity, error)
	IncrementError(ctx context.Context, entityType CodeEntityType, entityName, service string) error
	IncrementInvestigation(ctx context.Context, entityType CodeEntityType, entityName, service string) error
	BatchRecomputeRisk(ctx context.Context) error
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// TestCorrelationStore manages uncovered error path analysis.
type TestCorrelationStore interface {
	RefreshUncoveredPaths(ctx context.Context) error
	TopByPriority(ctx context.Context, service string, limit int) ([]UncoveredErrorPath, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*UncoveredErrorPath, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
