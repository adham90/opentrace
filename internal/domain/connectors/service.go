package connectors

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides data source connection management.
type Service struct {
	repo DataSourceRepository
}

// NewService creates a connectors service.
func NewService(repo DataSourceRepository) *Service {
	return &Service{repo: repo}
}

// List returns all configured data sources.
func (s *Service) List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("connectors list: %w", domain.ErrNotConfigured)
	}
	return s.repo.List(ctx, params)
}

// Get returns a single data source by ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*store.DataSource, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("connectors get: %w", domain.ErrNotConfigured)
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("data source id: %w", domain.ErrInvalidInput)
	}
	ds, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting data source %s: %w", id, err)
	}
	return ds, nil
}

// Create adds a new data source configuration.
func (s *Service) Create(ctx context.Context, params store.CreateDataSourceParams) (*store.DataSource, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("connectors create: %w", domain.ErrNotConfigured)
	}
	if params.Name == "" {
		return nil, fmt.Errorf("name: %w", domain.ErrInvalidInput)
	}
	if params.Type == "" {
		return nil, fmt.Errorf("type: %w", domain.ErrInvalidInput)
	}
	ds, err := s.repo.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("creating data source: %w", err)
	}
	return ds, nil
}

// Update modifies an existing data source configuration.
func (s *Service) Update(ctx context.Context, id uuid.UUID, params store.UpdateDataSourceParams) (*store.DataSource, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("connectors update: %w", domain.ErrNotConfigured)
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("data source id: %w", domain.ErrInvalidInput)
	}
	ds, err := s.repo.Update(ctx, id, params)
	if err != nil {
		return nil, fmt.Errorf("updating data source %s: %w", id, err)
	}
	return ds, nil
}

// Delete removes a data source configuration.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if s.repo == nil {
		return fmt.Errorf("connectors delete: %w", domain.ErrNotConfigured)
	}
	if id == uuid.Nil {
		return fmt.Errorf("data source id: %w", domain.ErrInvalidInput)
	}
	return s.repo.Delete(ctx, id)
}
