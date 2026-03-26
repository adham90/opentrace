package store

import (
	"context"
	"time"
)

// HealthCheckStore manages HTTP endpoint health checks (uptime monitoring).
type HealthCheckStore interface {
	Create(ctx context.Context, params CreateHealthCheckParams) (*HealthCheck, error)
	Get(ctx context.Context, id string) (*HealthCheck, error)
	List(ctx context.Context, params ListHealthCheckParams) ([]HealthCheck, error)
	Delete(ctx context.Context, id string) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	RecordResult(ctx context.Context, result HealthCheckResult) error
	LatestResults(ctx context.Context, healthcheckID string, limit int) ([]HealthCheckResult, error)
	UptimeSummaries(ctx context.Context, since time.Time) ([]UptimeSummary, error)
	PruneResults(ctx context.Context, olderThan time.Duration) (int64, error)
}
