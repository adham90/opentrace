// Package healthchecks provides the domain service for HTTP endpoint
// uptime monitoring and health check management.
package healthchecks

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Repository defines data access for health check operations.
// Implemented by internal/db.HealthCheckStore (SQLite adapter).
type Repository interface {
	List(ctx context.Context, params store.ListHealthCheckParams) ([]store.HealthCheck, error)
	Create(ctx context.Context, params store.CreateHealthCheckParams) (*store.HealthCheck, error)
	Delete(ctx context.Context, id string) error
	RecordResult(ctx context.Context, result store.HealthCheckResult) error
	UptimeSummaries(ctx context.Context, since time.Time) ([]store.UptimeSummary, error)
}
