package logs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fake implements Repository for testing.
type fake struct {
	entries    []store.LogEntry
	levels     map[string]int
	services   []store.ServiceLogCount
	searchErr  error
	countErr   error
	values     []string
	keys       []string
	summaries  []store.RequestSummaryResult
}

func (f *fake) Search(_ context.Context, _ store.LogSearchParams) ([]store.LogEntry, error) {
	return f.entries, f.searchErr
}

func (f *fake) GetByID(_ context.Context, id int64) (*store.LogEntry, error) {
	for i := range f.entries {
		if f.entries[i].ID == id {
			return &f.entries[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fake) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return f.levels, f.countErr
}

func (f *fake) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return f.services, nil
}

func (f *fake) Histogram(_ context.Context, _ store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}

func (f *fake) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) {
	return f.values, nil
}

func (f *fake) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	return f.keys, nil
}

func (f *fake) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return f.summaries, nil
}

// Compile-time check.
var _ Repository = (*fake)(nil)

// --- Search ---

func TestSearch_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Search(context.Background(), store.LogSearchParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	svc := NewService(&fake{})
	result, err := svc.Search(context.Background(), store.LogSearchParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 total, got %d", result.Total)
	}
}

func TestSearch_WithResults(t *testing.T) {
	now := time.Now()
	f := &fake{
		entries: []store.LogEntry{
			{ID: 1, Message: "hello", Level: "info", Timestamp: now},
			{ID: 2, Message: "world", Level: "error", Timestamp: now},
		},
	}
	svc := NewService(f)
	result, err := svc.Search(context.Background(), store.LogSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected 2 total, got %d", result.Total)
	}
}

func TestSearch_LimitCapped(t *testing.T) {
	svc := NewService(&fake{})
	// Limit over 500 should be capped.
	result, err := svc.Search(context.Background(), store.LogSearchParams{Limit: 9999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSearch_StoreError(t *testing.T) {
	f := &fake{searchErr: errors.New("db down")}
	svc := NewService(f)
	_, err := svc.Search(context.Background(), store.LogSearchParams{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- GetByID ---

func TestGetByID_Found(t *testing.T) {
	f := &fake{entries: []store.LogEntry{{ID: 42, Message: "test"}}}
	svc := NewService(f)
	entry, err := svc.GetByID(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Message != "test" {
		t.Errorf("message = %q, want 'test'", entry.Message)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.GetByID(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- CountByLevel ---

func TestCountByLevel_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.CountByLevel(context.Background(), store.LogCountParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestCountByLevel_WithData(t *testing.T) {
	f := &fake{levels: map[string]int{"INFO": 100, "ERROR": 10, "FATAL": 2}}
	svc := NewService(f)
	counts, err := svc.CountByLevel(context.Background(), store.LogCountParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counts.Total != 112 {
		t.Errorf("total = %d, want 112", counts.Total)
	}
	if counts.ErrorCount != 12 {
		t.Errorf("error_count = %d, want 12", counts.ErrorCount)
	}
	if counts.ErrorRate < 10.7 || counts.ErrorRate > 10.8 {
		t.Errorf("error_rate = %.1f, want ~10.7", counts.ErrorRate)
	}
}

func TestCountByLevel_ZeroTotal(t *testing.T) {
	f := &fake{levels: map[string]int{}}
	svc := NewService(f)
	counts, err := svc.CountByLevel(context.Background(), store.LogCountParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counts.ErrorRate != 0 {
		t.Errorf("error_rate should be 0 for zero total, got %f", counts.ErrorRate)
	}
}

// --- Trace ---

func TestTrace_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Trace(context.Background(), "abc")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestTrace_EmptyTraceID(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.Trace(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestTrace_WithEntries(t *testing.T) {
	now := time.Now()
	f := &fake{
		entries: []store.LogEntry{
			{ID: 1, Service: "api", Level: "info", Timestamp: now},
			{ID: 2, Service: "db", Level: "error", Timestamp: now.Add(100 * time.Millisecond)},
			{ID: 3, Service: "api", Level: "info", Timestamp: now.Add(200 * time.Millisecond)},
		},
	}
	svc := NewService(f)
	result, err := svc.Trace(context.Background(), "trace-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalEntries != 3 {
		t.Errorf("total = %d, want 3", result.TotalEntries)
	}
	if !result.HasErrors {
		t.Error("expected HasErrors=true")
	}
	if len(result.Services) != 2 {
		t.Errorf("services = %v, want 2 services", result.Services)
	}
	if result.TotalDurationMs < 190 || result.TotalDurationMs > 210 {
		t.Errorf("duration = %d, want ~200ms", result.TotalDurationMs)
	}
}

func TestTrace_NoEntries(t *testing.T) {
	svc := NewService(&fake{})
	result, err := svc.Trace(context.Background(), "missing-trace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalEntries != 0 {
		t.Errorf("total = %d, want 0", result.TotalEntries)
	}
}

// --- Performance ---

func TestPerformance_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Performance(context.Background(), time.Now(), time.Now(), "")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestPerformance_Empty(t *testing.T) {
	svc := NewService(&fake{})
	result, err := svc.Performance(context.Background(), time.Now().Add(-time.Hour), time.Now(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalRequests != 0 {
		t.Errorf("total = %d, want 0", result.TotalRequests)
	}
}
