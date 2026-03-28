// Package auth provides the domain service for user authentication and
// MCP token validation. The repository interface defines what the service
// needs from the data layer.
package auth

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

// UserRepository defines data access for user operations.
// Implemented by internal/db.UserStore (SQLite adapter).
type UserRepository interface {
	GetByID(ctx context.Context, id string) (*store.User, error)
	GetByEmail(ctx context.Context, email string) (*store.User, error)
	GetByMCPToken(ctx context.Context, token string) (*store.User, error)
	Create(ctx context.Context, params store.CreateUserParams) (*store.User, error)
	Update(ctx context.Context, id string, params store.UpdateUserParams) (*store.User, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]store.User, error)
	Count(ctx context.Context) (int, error)
}
