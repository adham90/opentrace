// Package watches provides the domain service for metric threshold monitors
// (watches) and their alerts.
package watches

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

// Repository defines data access for watch operations.
// Implemented by internal/db.WatchStore (SQLite adapter).
type Repository interface {
	List(ctx context.Context, params store.ListWatchParams) ([]store.Watch, error)
	GetByID(ctx context.Context, id string) (*store.Watch, error)
	Create(ctx context.Context, params store.CreateWatchParams) (*store.Watch, error)
	Delete(ctx context.Context, id string) error
	GetAlert(ctx context.Context, id string) (*store.WatchAlert, error)
	ListAlerts(ctx context.Context, watchID string, status string, limit int) ([]store.WatchAlert, error)
	CountPendingAlerts(ctx context.Context) (int, error)
	DismissAlert(ctx context.Context, id string, reason string) error
	AcknowledgeAlert(ctx context.Context, id string) error
}
