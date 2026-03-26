package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServerStore manages monitored server registrations.
type ServerStore interface {
	Register(ctx context.Context, params RegisterServerParams) (*Server, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Server, error)
	List(ctx context.Context, params ListServerParams) ([]Server, error)
	Update(ctx context.Context, id uuid.UUID, params UpdateServerParams) (*Server, error)
	UpdateHeartbeat(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	MarkStaleOffline(ctx context.Context, threshold time.Duration) (int, error)
}

// MetricStore manages time-series metric data for servers.
type MetricStore interface {
	BatchInsert(ctx context.Context, serverID uuid.UUID, ts time.Time, samples []MetricSample) (int, error)
	Query(ctx context.Context, params MetricQuery) ([]MetricPoint, error)
	LatestByServer(ctx context.Context, serverID uuid.UUID) ([]MetricPoint, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
