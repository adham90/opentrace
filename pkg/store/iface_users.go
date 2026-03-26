package store

import (
	"context"
	"time"
)

// UserStore manages user accounts.
type UserStore interface {
	Create(ctx context.Context, params CreateUserParams) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByMCPToken(ctx context.Context, token string) (*User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, id string, params UpdateUserParams) (*User, error)
	UpdatePassword(ctx context.Context, id string, passwordHash string) error
	UpdateMCPToken(ctx context.Context, id string, token string) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
}

// SessionStore manages browser sessions.
type SessionStore interface {
	Create(ctx context.Context, userID string, token string, expiresAt time.Time) (*Session, error)
	GetByToken(ctx context.Context, token string) (*Session, error)
	Delete(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context) (int, error)
	DeleteAllForUser(ctx context.Context, userID string) error
}
