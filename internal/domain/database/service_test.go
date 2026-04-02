package database

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
)

// fakeExec implements QueryExecutor for testing.
type fakeExec struct {
	result *QueryResult
	err    error
}

func (f *fakeExec) ExecuteReadQuery(_ context.Context, _ string) (*QueryResult, error) {
	return f.result, f.err
}

// Compile-time check.
var _ QueryExecutor = (*fakeExec)(nil)

// --- Schema ---

func TestSchema_NilExecutor(t *testing.T) {
	svc := &Service{}
	_, err := svc.Schema(context.Background(), "SELECT 1")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestSchema_EmptyQuery(t *testing.T) {
	svc := NewService(&fakeExec{})
	_, err := svc.Schema(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestSchema_Success(t *testing.T) {
	exec := &fakeExec{
		result: &QueryResult{
			Columns:  []string{"table_name"},
			Rows:     [][]any{{"users"}, {"logs"}},
			RowCount: 2,
		},
	}
	svc := NewService(exec)
	result, err := svc.Schema(context.Background(), "SELECT table_name FROM information_schema.tables")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 2 {
		t.Errorf("row_count = %d, want 2", result.RowCount)
	}
}

func TestSchema_ExecutorError(t *testing.T) {
	exec := &fakeExec{err: errors.New("connection refused")}
	svc := NewService(exec)
	_, err := svc.Schema(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Explain ---

func TestExplain_NilExecutor(t *testing.T) {
	svc := &Service{}
	_, err := svc.Explain(context.Background(), "SELECT 1")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestExplain_EmptyQuery(t *testing.T) {
	svc := NewService(&fakeExec{})
	_, err := svc.Explain(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

// --- Activity ---

func TestActivity_NilExecutor(t *testing.T) {
	svc := &Service{}
	_, err := svc.Activity(context.Background(), "SELECT * FROM pg_stat_activity")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestActivity_Success(t *testing.T) {
	exec := &fakeExec{
		result: &QueryResult{
			Columns:  []string{"pid", "state"},
			Rows:     [][]any{{1234, "active"}},
			RowCount: 1,
		},
	}
	svc := NewService(exec)
	result, err := svc.Activity(context.Background(), "SELECT pid, state FROM pg_stat_activity")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 1 {
		t.Errorf("row_count = %d, want 1", result.RowCount)
	}
}
