package servers

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/google/uuid"
)

// Service provides server management and metric querying.
type Service struct {
	servers ServerRepository
	metrics MetricRepository
}

// NewService creates a servers service.
func NewService(servers ServerRepository, metrics MetricRepository) *Service {
	return &Service{servers: servers, metrics: metrics}
}

// List returns registered servers.
func (s *Service) List(ctx context.Context, params store.ListServerParams) ([]store.Server, error) {
	if s.servers == nil {
		return nil, fmt.Errorf("list servers: %w", domain.ErrNotConfigured)
	}
	params.Limit = domain.ClampLimit(params.Limit, 50, 200)
	servers, err := s.servers.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing servers: %w", err)
	}
	return servers, nil
}

// Register adds or updates a server registration.
func (s *Service) Register(ctx context.Context, params store.RegisterServerParams) (*store.Server, error) {
	if s.servers == nil {
		return nil, fmt.Errorf("register server: %w", domain.ErrNotConfigured)
	}
	if params.Hostname == "" {
		return nil, fmt.Errorf("hostname: %w", domain.ErrInvalidInput)
	}
	srv, err := s.servers.Register(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("registering server: %w", err)
	}
	return srv, nil
}

// Health returns the latest metrics for a server.
func (s *Service) Health(ctx context.Context, serverID uuid.UUID) ([]store.MetricPoint, error) {
	if s.metrics == nil {
		return nil, fmt.Errorf("server health: %w", domain.ErrNotConfigured)
	}
	if serverID == uuid.Nil {
		return nil, fmt.Errorf("server_id: %w", domain.ErrInvalidInput)
	}
	points, err := s.metrics.LatestByServer(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("fetching health for %s: %w", serverID, err)
	}
	return points, nil
}

// QueryMetrics returns metric time series matching the given query.
func (s *Service) QueryMetrics(ctx context.Context, params store.MetricQuery) ([]store.MetricPoint, error) {
	if s.metrics == nil {
		return nil, fmt.Errorf("query metrics: %w", domain.ErrNotConfigured)
	}
	if params.ServerID == uuid.Nil {
		return nil, fmt.Errorf("server_id: %w", domain.ErrInvalidInput)
	}
	params.Limit = domain.ClampLimit(params.Limit, 100, 500)
	points, err := s.metrics.Query(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("querying metrics: %w", err)
	}
	return points, nil
}
