package code

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides code entity risk analysis and test correlation.
type Service struct {
	entities CodeEntityRepository
	tests    TestCorrelationRepository
	notes    NoteRepository
}

// NewService creates a code intelligence service.
func NewService(entities CodeEntityRepository, tests TestCorrelationRepository, notes NoteRepository) *Service {
	return &Service{entities: entities, tests: tests, notes: notes}
}

// RiskResult holds the top risky code entities.
type RiskResult struct {
	Entities []store.CodeEntity `json:"entities"`
	Total    int                `json:"total"`
}

// Risk returns the top code entities ranked by risk score.
func (s *Service) Risk(ctx context.Context, service string, limit int) (*RiskResult, error) {
	if s.entities == nil {
		return nil, fmt.Errorf("code risk: %w", domain.ErrNotConfigured)
	}

	limit = domain.ClampLimit(limit, 20, 100)

	entities, err := s.entities.TopByRisk(ctx, service, limit)
	if err != nil {
		return nil, fmt.Errorf("fetching risky entities: %w", err)
	}

	return &RiskResult{
		Entities: entities,
		Total:    len(entities),
	}, nil
}

// FragileResult holds uncovered error paths that need test coverage.
type FragileResult struct {
	Paths []store.UncoveredErrorPath `json:"paths"`
	Total int                        `json:"total"`
}

// Fragile returns error paths that lack test coverage.
func (s *Service) Fragile(ctx context.Context, service string, limit int) (*FragileResult, error) {
	if s.tests == nil {
		return nil, fmt.Errorf("code fragile: %w", domain.ErrNotConfigured)
	}

	limit = domain.ClampLimit(limit, 20, 100)

	paths, err := s.tests.TopByPriority(ctx, service, limit)
	if err != nil {
		return nil, fmt.Errorf("fetching fragile paths: %w", err)
	}

	return &FragileResult{
		Paths: paths,
		Total: len(paths),
	}, nil
}

// Annotate adds or updates an agent note on a code entity.
func (s *Service) Annotate(ctx context.Context, entityType, entityID, note string) (*store.AgentNote, error) {
	if s.notes == nil {
		return nil, fmt.Errorf("code annotate: %w", domain.ErrNotConfigured)
	}
	if entityType == "" || entityID == "" {
		return nil, fmt.Errorf("entity_type and entity_id: %w", domain.ErrInvalidInput)
	}
	if note == "" {
		return nil, fmt.Errorf("note: %w", domain.ErrInvalidInput)
	}

	result, err := s.notes.Upsert(ctx, entityType, entityID, note)
	if err != nil {
		return nil, fmt.Errorf("upserting annotation: %w", err)
	}
	return result, nil
}

// Annotations lists all agent notes for a given entity type.
func (s *Service) Annotations(ctx context.Context, entityType string) ([]store.AgentNote, error) {
	if s.notes == nil {
		return nil, fmt.Errorf("code annotations: %w", domain.ErrNotConfigured)
	}
	return s.notes.List(ctx, entityType)
}
