// Package deploys provides the domain service for deployment tracking
// and impact measurement.
package deploys

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

// Repository defines data access for deploy operations.
// Implemented by internal/db.DeployStore (SQLite adapter).
type Repository interface {
	Create(ctx context.Context, params store.CreateDeployParams) (*store.Deploy, error)
	GetByID(ctx context.Context, id int64) (*store.Deploy, error)
	GetByCommit(ctx context.Context, commitHash string) (*store.Deploy, error)
	GetRecent(ctx context.Context, service string, limit int) ([]store.Deploy, error)
}
