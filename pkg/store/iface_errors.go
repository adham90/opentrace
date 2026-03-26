package store

import (
	"context"
	"time"
)

// ErrorGroupStore manages error groups aggregated by fingerprint.
type ErrorGroupStore interface {
	Upsert(ctx context.Context, entry LogEntry) error
	Get(ctx context.Context, fingerprint string) (*ErrorGroup, error)
	List(ctx context.Context, params ListErrorGroupParams) ([]ErrorGroup, error)
	Count(ctx context.Context, status ErrorGroupStatus) (int, error)
	Resolve(ctx context.Context, fingerprint string, reason string) error
	Ignore(ctx context.Context, fingerprint string, reason string) error
	ListEvents(ctx context.Context, fingerprint string, limit int) ([]ErrorGroupEvent, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// ErrorImpactStore tracks which users are affected by each error and computes impact scores.
type ErrorImpactStore interface {
	// Called on each error occurrence (upsert)
	TrackImpact(ctx context.Context, fingerprint string, userID string, contextData map[string]any, logID int64, service string) error

	// Query
	GetImpact(ctx context.Context, fingerprint string) (*ErrorImpact, error)
	GetAffectedUsers(ctx context.Context, fingerprint string, limit int) ([]AffectedUser, error)
	GetUserErrors(ctx context.Context, userID string, since time.Time) ([]ErrorSummary, error)

	// Scoring
	ComputeImpactScores(ctx context.Context) error
	TopByImpact(ctx context.Context, params ImpactQueryParams) ([]ErrorGroupWithImpact, error)

	// Pattern detection
	FindCommonTraits(ctx context.Context, fingerprint string) (map[string]any, error)
}
