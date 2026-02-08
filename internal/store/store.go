package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// DataSourceStore defines CRUD operations for data sources.
type DataSourceStore interface {
	Create(ctx context.Context, params CreateDataSourceParams) (*DataSource, error)
	GetByID(ctx context.Context, id uuid.UUID) (*DataSource, error)
	List(ctx context.Context) ([]DataSource, error)
	Update(ctx context.Context, id uuid.UUID, params UpdateDataSourceParams) (*DataSource, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
