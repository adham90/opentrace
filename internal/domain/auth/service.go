package auth

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides user authentication and MCP token validation.
type Service struct {
	repo UserRepository
}

// NewService creates an auth service.
func NewService(repo UserRepository) *Service {
	return &Service{repo: repo}
}

// Authenticate looks up a user by email. The caller is responsible for
// password verification — this service only retrieves the user record.
func (s *Service) Authenticate(ctx context.Context, email string) (*store.User, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("auth authenticate: %w", domain.ErrNotConfigured)
	}
	if email == "" {
		return nil, fmt.Errorf("email: %w", domain.ErrInvalidInput)
	}

	user, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("authenticating user: %w", err)
	}
	return user, nil
}

// ValidateMCPToken looks up a user by their MCP token.
func (s *Service) ValidateMCPToken(ctx context.Context, token string) (*store.User, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("auth validate token: %w", domain.ErrNotConfigured)
	}
	if token == "" {
		return nil, fmt.Errorf("token: %w", domain.ErrInvalidInput)
	}

	user, err := s.repo.GetByMCPToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("validating MCP token: %w", err)
	}
	return user, nil
}

// ListUsers returns all registered users.
func (s *Service) ListUsers(ctx context.Context) ([]store.User, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("auth list users: %w", domain.ErrNotConfigured)
	}
	return s.repo.List(ctx)
}

// CreateUser registers a new user account.
func (s *Service) CreateUser(ctx context.Context, params store.CreateUserParams) (*store.User, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("auth create user: %w", domain.ErrNotConfigured)
	}
	if params.Email == "" {
		return nil, fmt.Errorf("email: %w", domain.ErrInvalidInput)
	}

	user, err := s.repo.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return user, nil
}

// GetUser retrieves a user by ID.
func (s *Service) GetUser(ctx context.Context, id string) (*store.User, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("auth get user: %w", domain.ErrNotConfigured)
	}
	if id == "" {
		return nil, fmt.Errorf("user id: %w", domain.ErrInvalidInput)
	}

	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting user %s: %w", id, err)
	}
	return user, nil
}

// DeleteUser removes a user account.
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	if s.repo == nil {
		return fmt.Errorf("auth delete user: %w", domain.ErrNotConfigured)
	}
	if id == "" {
		return fmt.Errorf("user id: %w", domain.ErrInvalidInput)
	}
	return s.repo.Delete(ctx, id)
}
