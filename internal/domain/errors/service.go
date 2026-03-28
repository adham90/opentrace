package errors

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides error group queries, resolution lifecycle, and impact analysis.
type Service struct {
	repo   Repository
	impact ImpactRepository
}

// NewService creates an error service. Either dependency may be nil; methods
// that need them return domain.ErrNotConfigured.
func NewService(repo Repository, impact ImpactRepository) *Service {
	return &Service{repo: repo, impact: impact}
}

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

// ListResult is the result of listing error groups.
type ListResult struct {
	TotalUnresolved int            `json:"total_unresolved"`
	Returned        int            `json:"returned"`
	ErrorGroups     []GroupSummary `json:"error_groups"`
}

// GroupSummary is a compact view of an error group.
type GroupSummary struct {
	Fingerprint     string `json:"fingerprint"`
	Service         string `json:"service"`
	Environment     string `json:"environment,omitempty"`
	ExceptionClass  string `json:"exception_class,omitempty"`
	Message         string `json:"message"`
	Status          string `json:"status"`
	OccurrenceCount int    `json:"occurrence_count"`
	LastSeenAt      string `json:"last_seen_at"`
	FirstSeenAt     string `json:"first_seen_at"`
	ReopenedCount   int    `json:"reopened_count,omitempty"`
}

// DetailResult holds the full error group with lifecycle events.
type DetailResult struct {
	Group  store.ErrorGroup      `json:"group"`
	Events []store.ErrorGroupEvent `json:"events"`
}

// ImpactResult holds the impact analysis for an error group.
type ImpactResult struct {
	Impact        *store.ErrorImpact   `json:"impact"`
	AffectedUsers []store.AffectedUser `json:"affected_users,omitempty"`
}

// ListParams controls filtering and pagination for error group listing.
type ListParams struct {
	Status      string
	Service     string
	Environment string
	SortBy      string
	Limit       int
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// List returns error groups matching the given params.
func (s *Service) List(ctx context.Context, p ListParams) (*ListResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("error list: %w", domain.ErrNotConfigured)
	}

	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.Limit > 100 {
		p.Limit = 100
	}

	params := store.ListErrorGroupParams{
		Limit: p.Limit,
	}
	if p.Status != "" {
		params.Status = store.ErrorGroupStatus(p.Status)
	}
	params.Service = p.Service
	params.Environment = p.Environment
	if p.SortBy != "" {
		params.SortBy = p.SortBy
	}

	groups, err := s.repo.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing error groups: %w", err)
	}

	unresolvedCount, _ := s.repo.Count(ctx, store.ErrorGroupUnresolved)

	summaries := make([]GroupSummary, len(groups))
	for i, g := range groups {
		summaries[i] = GroupSummary{
			Fingerprint:     g.Fingerprint,
			Service:         g.Service,
			Environment:     g.Environment,
			ExceptionClass:  g.ExceptionClass,
			Message:         g.Message,
			Status:          string(g.Status),
			OccurrenceCount: g.OccurrenceCount,
			LastSeenAt:      g.LastSeenAt.Format(time.RFC3339),
			FirstSeenAt:     g.FirstSeenAt.Format(time.RFC3339),
			ReopenedCount:   g.ReopenedCount,
		}
	}

	return &ListResult{
		TotalUnresolved: unresolvedCount,
		Returned:        len(summaries),
		ErrorGroups:     summaries,
	}, nil
}

// ---------------------------------------------------------------------------
// Detail
// ---------------------------------------------------------------------------

// Detail fetches an error group and its recent lifecycle events.
func (s *Service) Detail(ctx context.Context, fingerprint string) (*DetailResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("error detail: %w", domain.ErrNotConfigured)
	}
	if fingerprint == "" {
		return nil, fmt.Errorf("fingerprint: %w", domain.ErrInvalidInput)
	}

	eg, err := s.repo.Get(ctx, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("getting error group %s: %w", fingerprint, err)
	}
	if eg == nil {
		return nil, fmt.Errorf("error group %s: %w", fingerprint, domain.ErrNotFound)
	}

	events, _ := s.repo.ListEvents(ctx, fingerprint, 10)

	return &DetailResult{
		Group:  *eg,
		Events: events,
	}, nil
}

// ---------------------------------------------------------------------------
// Resolve / Ignore
// ---------------------------------------------------------------------------

// Resolve marks an error group as resolved with the given reason.
func (s *Service) Resolve(ctx context.Context, fingerprint, reason string) error {
	if s.repo == nil {
		return fmt.Errorf("error resolve: %w", domain.ErrNotConfigured)
	}
	if fingerprint == "" {
		return fmt.Errorf("fingerprint: %w", domain.ErrInvalidInput)
	}
	if reason == "" {
		return fmt.Errorf("reason: %w", domain.ErrInvalidInput)
	}
	return s.repo.Resolve(ctx, fingerprint, reason)
}

// Ignore marks an error group as permanently ignored with the given reason.
func (s *Service) Ignore(ctx context.Context, fingerprint, reason string) error {
	if s.repo == nil {
		return fmt.Errorf("error ignore: %w", domain.ErrNotConfigured)
	}
	if fingerprint == "" {
		return fmt.Errorf("fingerprint: %w", domain.ErrInvalidInput)
	}
	if reason == "" {
		return fmt.Errorf("reason: %w", domain.ErrInvalidInput)
	}
	return s.repo.Ignore(ctx, fingerprint, reason)
}

// ---------------------------------------------------------------------------
// Impact
// ---------------------------------------------------------------------------

// Impact returns impact analysis for an error group, including affected users.
func (s *Service) Impact(ctx context.Context, fingerprint string, userLimit int) (*ImpactResult, error) {
	if s.impact == nil {
		return nil, fmt.Errorf("error impact: %w", domain.ErrNotConfigured)
	}
	if fingerprint == "" {
		return nil, fmt.Errorf("fingerprint: %w", domain.ErrInvalidInput)
	}

	if userLimit <= 0 {
		userLimit = 10
	}
	if userLimit > 100 {
		userLimit = 100
	}

	impact, err := s.impact.GetImpact(ctx, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("getting impact for %s: %w", fingerprint, err)
	}

	users, _ := s.impact.GetAffectedUsers(ctx, fingerprint, userLimit)

	return &ImpactResult{
		Impact:        impact,
		AffectedUsers: users,
	}, nil
}
