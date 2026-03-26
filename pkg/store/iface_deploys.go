package store

import (
	"context"
	"time"
)

// DeployStore manages deploy lifecycle and impact measurement (Stage 5).
type DeployStore interface {
	Create(ctx context.Context, params CreateDeployParams) (*Deploy, error)
	GetByID(ctx context.Context, id int64) (*Deploy, error)
	GetByCommit(ctx context.Context, commitHash string) (*Deploy, error)
	GetRecent(ctx context.Context, service string, limit int) ([]Deploy, error)
	MeasureImpact(ctx context.Context, id int64, impact DeployImpact) error
	LinkInvestigation(ctx context.Context, id int64, sessionID string) error
	GetPendingMeasurement(ctx context.Context, olderThan time.Duration) ([]Deploy, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
