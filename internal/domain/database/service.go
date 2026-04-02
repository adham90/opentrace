package database

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
)

// Service provides external database introspection (schema, queries, activity).
type Service struct {
	exec QueryExecutor
}

// NewService creates a database service.
func NewService(exec QueryExecutor) *Service {
	return &Service{exec: exec}
}

// SchemaResult holds the output of a schema introspection query.
type SchemaResult struct {
	Columns  []string `json:"columns"`
	Rows     [][]any  `json:"rows"`
	RowCount int      `json:"row_count"`
}

// Schema lists tables/columns from the connected data source.
func (s *Service) Schema(ctx context.Context, query string) (*SchemaResult, error) {
	if s.exec == nil {
		return nil, fmt.Errorf("database schema: %w", domain.ErrNotConfigured)
	}
	if query == "" {
		return nil, fmt.Errorf("query: %w", domain.ErrInvalidInput)
	}

	result, err := s.exec.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("executing schema query: %w", err)
	}

	return &SchemaResult{
		Columns:  result.Columns,
		Rows:     result.Rows,
		RowCount: result.RowCount,
	}, nil
}

// Tables lists all tables by running the provided discovery query.
func (s *Service) Tables(ctx context.Context, query string) (*SchemaResult, error) {
	if s.exec == nil {
		return nil, fmt.Errorf("database tables: %w", domain.ErrNotConfigured)
	}
	if query == "" {
		return nil, fmt.Errorf("query: %w", domain.ErrInvalidInput)
	}

	result, err := s.exec.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}

	return &SchemaResult{
		Columns:  result.Columns,
		Rows:     result.Rows,
		RowCount: result.RowCount,
	}, nil
}

// Explain runs an EXPLAIN on the given query and returns the plan.
func (s *Service) Explain(ctx context.Context, query string) (*SchemaResult, error) {
	if s.exec == nil {
		return nil, fmt.Errorf("database explain: %w", domain.ErrNotConfigured)
	}
	if query == "" {
		return nil, fmt.Errorf("query: %w", domain.ErrInvalidInput)
	}

	result, err := s.exec.ExecuteReadQuery(ctx, "EXPLAIN "+query)
	if err != nil {
		return nil, fmt.Errorf("explaining query: %w", err)
	}

	return &SchemaResult{
		Columns:  result.Columns,
		Rows:     result.Rows,
		RowCount: result.RowCount,
	}, nil
}

// Activity returns current database activity (connections, locks, etc.).
func (s *Service) Activity(ctx context.Context, query string) (*SchemaResult, error) {
	if s.exec == nil {
		return nil, fmt.Errorf("database activity: %w", domain.ErrNotConfigured)
	}
	if query == "" {
		return nil, fmt.Errorf("query: %w", domain.ErrInvalidInput)
	}

	result, err := s.exec.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying activity: %w", err)
	}

	return &SchemaResult{
		Columns:  result.Columns,
		Rows:     result.Rows,
		RowCount: result.RowCount,
	}, nil
}
