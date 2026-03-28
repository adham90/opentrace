// Package errors provides business logic for the error management domain.
// It queries error group stores and returns typed results, independent of
// any transport (MCP, HTTP, CLI).
package errors

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Service provides error group queries and aggregation.
type Service struct {
	ErrorGroups store.ErrorGroupStore
}

// ListParams controls filtering and pagination for error group listing.
type ListParams struct {
	Status      string
	Service     string
	Environment string
	SortBy      string
	Limit       int
}

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

// List returns error groups matching the given params.
func (s *Service) List(ctx context.Context, p ListParams) (*ListResult, error) {
	if s.ErrorGroups == nil {
		return nil, fmt.Errorf("ErrorGroupStore not configured")
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

	groups, err := s.ErrorGroups.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list error groups: %w", err)
	}

	unresolvedCount, _ := s.ErrorGroups.Count(ctx, store.ErrorGroupUnresolved)

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
