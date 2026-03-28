// Package errors provides the domain service for error group management,
// resolution lifecycle, and impact analysis. The repository interfaces define
// what the service needs from data stores.
package errors

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Repository defines data access for error group operations.
// Implemented by internal/db.ErrorGroupStore (SQLite adapter).
type Repository interface {
	List(ctx context.Context, params store.ListErrorGroupParams) ([]store.ErrorGroup, error)
	Get(ctx context.Context, fingerprint string) (*store.ErrorGroup, error)
	Count(ctx context.Context, status store.ErrorGroupStatus) (int, error)
	Resolve(ctx context.Context, fingerprint string, reason string) error
	Ignore(ctx context.Context, fingerprint string, reason string) error
	ListEvents(ctx context.Context, fingerprint string, limit int) ([]store.ErrorGroupEvent, error)
}

// ImpactRepository defines data access for error impact analysis.
// Implemented by internal/db.ErrorImpactStore (SQLite adapter).
type ImpactRepository interface {
	GetImpact(ctx context.Context, fingerprint string) (*store.ErrorImpact, error)
	GetAffectedUsers(ctx context.Context, fingerprint string, limit int) ([]store.AffectedUser, error)
	GetUserErrors(ctx context.Context, userID string, since time.Time) ([]store.ErrorSummary, error)
}
