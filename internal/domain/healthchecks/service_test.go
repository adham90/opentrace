package healthchecks

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
	checks    []store.HealthCheck
	check     *store.HealthCheck
	summaries []store.UptimeSummary
	err       error
}

func (f *fake) List(_ context.Context, _ store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	return f.checks, f.err
}

func (f *fake) Create(_ context.Context, _ store.CreateHealthCheckParams) (*store.HealthCheck, error) {
	return f.check, f.err
}

func (f *fake) Delete(_ context.Context, _ string) error {
	return f.err
}

func (f *fake) RecordResult(_ context.Context, _ store.HealthCheckResult) error {
	return f.err
}

func (f *fake) UptimeSummaries(_ context.Context, _ time.Time) ([]store.UptimeSummary, error) {
	return f.summaries, f.err
}

var _ Repository = (*fake)(nil)

// --- List ---

func TestList_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.List(context.Background(), store.ListHealthCheckParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestList_HappyPath(t *testing.T) {
	f := &fake{
		checks: []store.HealthCheck{
			{ID: "hc1", Name: "API", URL: "https://api.example.com/health"},
			{ID: "hc2", Name: "Web", URL: "https://www.example.com"},
		},
	}
	svc := NewService(f)
	result, err := svc.List(context.Background(), store.ListHealthCheckParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d checks, want 2", len(result))
	}
}

func TestList_StoreError(t *testing.T) {
	f := &fake{err: errors.New("db down")}
	svc := NewService(f)
	_, err := svc.List(context.Background(), store.ListHealthCheckParams{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Create ---

func TestCreate_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Create(context.Background(), store.CreateHealthCheckParams{Name: "x", URL: "http://x"})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestCreate_MissingName(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.Create(context.Background(), store.CreateHealthCheckParams{URL: "http://x"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreate_MissingURL(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.Create(context.Background(), store.CreateHealthCheckParams{Name: "x"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreate_HappyPath(t *testing.T) {
	hc := &store.HealthCheck{ID: "hc1", Name: "API", URL: "https://api.example.com"}
	f := &fake{check: hc}
	svc := NewService(f)
	result, err := svc.Create(context.Background(), store.CreateHealthCheckParams{
		Name: "API",
		URL:  "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "hc1" {
		t.Errorf("id = %q, want hc1", result.ID)
	}
}

// --- Delete ---

func TestDelete_NilRepo(t *testing.T) {
	svc := &Service{}
	err := svc.Delete(context.Background(), "hc1")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestDelete_EmptyID(t *testing.T) {
	svc := NewService(&fake{})
	err := svc.Delete(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

// --- Uptime ---

func TestUptime_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Uptime(context.Background(), time.Now())
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestUptime_HappyPath(t *testing.T) {
	f := &fake{
		summaries: []store.UptimeSummary{
			{HealthCheckID: "hc1", Name: "API", UptimePct: 99.9},
		},
	}
	svc := NewService(f)
	result, err := svc.Uptime(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("got %d summaries, want 1", len(result))
	}
	if result[0].UptimePct != 99.9 {
		t.Errorf("uptime = %f, want 99.9", result[0].UptimePct)
	}
}
