// Package servers provides the domain service for monitored server
// registration, health tracking, and metric queries.
package servers

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
	"github.com/google/uuid"
)

// ServerRepository defines data access for server operations.
// Implemented by internal/db.ServerStore (SQLite adapter).
type ServerRepository interface {
	List(ctx context.Context, params store.ListServerParams) ([]store.Server, error)
	Register(ctx context.Context, params store.RegisterServerParams) (*store.Server, error)
	GetByID(ctx context.Context, id uuid.UUID) (*store.Server, error)
	Update(ctx context.Context, id uuid.UUID, params store.UpdateServerParams) (*store.Server, error)
}

// MetricRepository defines data access for server metric queries.
// Implemented by internal/db.MetricStore (SQLite adapter).
type MetricRepository interface {
	Query(ctx context.Context, params store.MetricQuery) ([]store.MetricPoint, error)
	LatestByServer(ctx context.Context, serverID uuid.UUID) ([]store.MetricPoint, error)
}
