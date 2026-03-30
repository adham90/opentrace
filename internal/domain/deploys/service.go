package deploys

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides deploy tracking and querying.
type Service struct {
	repo Repository
}

// NewService creates a deploys service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns recent deploys for a service.
func (s *Service) List(ctx context.Context, service string, limit int) ([]store.Deploy, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("list deploys: %w", domain.ErrNotConfigured)
	}
	limit = domain.ClampLimit(limit, 20, 200)
	deploys, err := s.repo.GetRecent(ctx, service, limit)
	if err != nil {
		return nil, fmt.Errorf("listing deploys: %w", err)
	}
	return deploys, nil
}

// Record creates a new deploy record.
func (s *Service) Record(ctx context.Context, params store.CreateDeployParams) (*store.Deploy, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("record deploy: %w", domain.ErrNotConfigured)
	}
	if params.Service == "" {
		return nil, fmt.Errorf("service: %w", domain.ErrInvalidInput)
	}
	if params.CommitHash == "" {
		return nil, fmt.Errorf("commit_hash: %w", domain.ErrInvalidInput)
	}
	deploy, err := s.repo.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("recording deploy: %w", err)
	}
	return deploy, nil
}

// GetByCommit retrieves a deploy by its commit hash.
func (s *Service) GetByCommit(ctx context.Context, commitHash string) (*store.Deploy, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("get deploy: %w", domain.ErrNotConfigured)
	}
	if commitHash == "" {
		return nil, fmt.Errorf("commit_hash: %w", domain.ErrInvalidInput)
	}
	deploy, err := s.repo.GetByCommit(ctx, commitHash)
	if err != nil {
		return nil, fmt.Errorf("getting deploy by commit %s: %w", commitHash, err)
	}
	return deploy, nil
}

// GetByID retrieves a deploy by its ID.
func (s *Service) GetByID(ctx context.Context, id int64) (*store.Deploy, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("get deploy: %w", domain.ErrNotConfigured)
	}
	deploy, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting deploy %d: %w", id, err)
	}
	return deploy, nil
}
