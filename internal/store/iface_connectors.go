package store

import (
	"context"

	"github.com/google/uuid"
)

// DataSourceStore defines CRUD operations for data sources.
type DataSourceStore interface {
	Create(ctx context.Context, params CreateDataSourceParams) (*DataSource, error)
	GetByID(ctx context.Context, id uuid.UUID) (*DataSource, error)
	List(ctx context.Context, params ListDataSourceParams) ([]DataSource, error)
	Update(ctx context.Context, id uuid.UUID, params UpdateDataSourceParams) (*DataSource, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
