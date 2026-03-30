// Package healthchecks provides the domain service for health check management
// (CRUD operations, uptime queries). For runtime scheduling and alerting, see
// internal/healthcheck.
package healthchecks

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides health check management and uptime reporting.
type Service struct {
	repo Repository
}

// NewService creates a healthchecks service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns all configured health checks.
func (s *Service) List(ctx context.Context, params store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("list healthchecks: %w", domain.ErrNotConfigured)
	}
	params.Limit = domain.ClampLimit(params.Limit, 50, 200)
	checks, err := s.repo.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing healthchecks: %w", err)
	}
	return checks, nil
}

// Create adds a new health check configuration.
func (s *Service) Create(ctx context.Context, params store.CreateHealthCheckParams) (*store.HealthCheck, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("create healthcheck: %w", domain.ErrNotConfigured)
	}
	if params.Name == "" {
		return nil, fmt.Errorf("name: %w", domain.ErrInvalidInput)
	}
	if params.URL == "" {
		return nil, fmt.Errorf("url: %w", domain.ErrInvalidInput)
	}
	check, err := s.repo.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("creating healthcheck: %w", err)
	}
	return check, nil
}

// Delete removes a health check by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	if s.repo == nil {
		return fmt.Errorf("delete healthcheck: %w", domain.ErrNotConfigured)
	}
	if id == "" {
		return fmt.Errorf("healthcheck id: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting healthcheck %s: %w", id, err)
	}
	return nil
}

// Uptime returns uptime summaries for all health checks since the given time.
func (s *Service) Uptime(ctx context.Context, since time.Time) ([]store.UptimeSummary, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("uptime summaries: %w", domain.ErrNotConfigured)
	}
	summaries, err := s.repo.UptimeSummaries(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("fetching uptime summaries: %w", err)
	}
	return summaries, nil
}
