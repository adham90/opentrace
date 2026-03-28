// Package connectors provides the domain service for managing external
// data source connections. The repository interface defines what the service
// needs from the data layer.
package connectors

import (
	"context"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/pkg/store"
)

// DataSourceRepository defines CRUD access for data source configurations.
// Implemented by internal/db.DataSourceStore (SQLite adapter).
type DataSourceRepository interface {
	List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error)
	GetByID(ctx context.Context, id uuid.UUID) (*store.DataSource, error)
	Create(ctx context.Context, params store.CreateDataSourceParams) (*store.DataSource, error)
	Update(ctx context.Context, id uuid.UUID, params store.UpdateDataSourceParams) (*store.DataSource, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
