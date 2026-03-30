package watches

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides watch management and alert handling.
type Service struct {
	repo Repository
}

// NewService creates a watches service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns watches matching the given filters.
func (s *Service) List(ctx context.Context, params store.ListWatchParams) ([]store.Watch, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("list watches: %w", domain.ErrNotConfigured)
	}
	params.Limit = domain.ClampLimit(params.Limit, 50, 200)
	watches, err := s.repo.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing watches: %w", err)
	}
	return watches, nil
}

// GetByID retrieves a single watch by ID.
func (s *Service) GetByID(ctx context.Context, id string) (*store.Watch, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("get watch: %w", domain.ErrNotConfigured)
	}
	if id == "" {
		return nil, fmt.Errorf("watch id: %w", domain.ErrInvalidInput)
	}
	w, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting watch %s: %w", id, err)
	}
	return w, nil
}

// Create creates a new watch from the given params.
func (s *Service) Create(ctx context.Context, params store.CreateWatchParams) (*store.Watch, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("create watch: %w", domain.ErrNotConfigured)
	}
	if params.Metric == "" {
		return nil, fmt.Errorf("metric: %w", domain.ErrInvalidInput)
	}
	w, err := s.repo.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("creating watch: %w", err)
	}
	return w, nil
}

// Delete removes a watch by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	if s.repo == nil {
		return fmt.Errorf("delete watch: %w", domain.ErrNotConfigured)
	}
	if id == "" {
		return fmt.Errorf("watch id: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting watch %s: %w", id, err)
	}
	return nil
}

// Alerts returns alerts for a watch, filtered by status.
func (s *Service) Alerts(ctx context.Context, watchID string, status string, limit int) ([]store.WatchAlert, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("list alerts: %w", domain.ErrNotConfigured)
	}
	limit = domain.ClampLimit(limit, 50, 200)
	alerts, err := s.repo.ListAlerts(ctx, watchID, status, limit)
	if err != nil {
		return nil, fmt.Errorf("listing alerts: %w", err)
	}
	return alerts, nil
}

// PendingAlertCount returns the number of unresolved alerts.
func (s *Service) PendingAlertCount(ctx context.Context) (int, error) {
	if s.repo == nil {
		return 0, fmt.Errorf("count pending alerts: %w", domain.ErrNotConfigured)
	}
	count, err := s.repo.CountPendingAlerts(ctx)
	if err != nil {
		return 0, fmt.Errorf("counting pending alerts: %w", err)
	}
	return count, nil
}

// Dismiss dismisses an alert with a reason.
func (s *Service) Dismiss(ctx context.Context, id string, reason string) error {
	if s.repo == nil {
		return fmt.Errorf("dismiss alert: %w", domain.ErrNotConfigured)
	}
	if id == "" {
		return fmt.Errorf("alert id: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.DismissAlert(ctx, id, reason); err != nil {
		return fmt.Errorf("dismissing alert %s: %w", id, err)
	}
	return nil
}

// Acknowledge marks an alert as acknowledged.
func (s *Service) Acknowledge(ctx context.Context, id string) error {
	if s.repo == nil {
		return fmt.Errorf("acknowledge alert: %w", domain.ErrNotConfigured)
	}
	if id == "" {
		return fmt.Errorf("alert id: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.AcknowledgeAlert(ctx, id); err != nil {
		return fmt.Errorf("acknowledging alert %s: %w", id, err)
	}
	return nil
}
