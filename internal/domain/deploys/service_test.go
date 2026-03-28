package deploys

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fake implements Repository for testing.
type fake struct {
	deploys []store.Deploy
	deploy  *store.Deploy
	err     error
}

func (f *fake) Create(_ context.Context, _ store.CreateDeployParams) (*store.Deploy, error) {
	return f.deploy, f.err
}

func (f *fake) GetByID(_ context.Context, _ int64) (*store.Deploy, error) {
	return f.deploy, f.err
}

func (f *fake) GetByCommit(_ context.Context, _ string) (*store.Deploy, error) {
	return f.deploy, f.err
}

func (f *fake) GetRecent(_ context.Context, _ string, _ int) ([]store.Deploy, error) {
	return f.deploys, f.err
}

var _ Repository = (*fake)(nil)

// --- List ---

func TestList_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.List(context.Background(), "", 10)
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestList_HappyPath(t *testing.T) {
	f := &fake{
		deploys: []store.Deploy{
			{ID: 1, Service: "api", CommitHash: "abc123"},
			{ID: 2, Service: "api", CommitHash: "def456"},
		},
	}
	svc := NewService(f)
	result, err := svc.List(context.Background(), "api", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d deploys, want 2", len(result))
	}
}

func TestList_StoreError(t *testing.T) {
	f := &fake{err: errors.New("db down")}
	svc := NewService(f)
	_, err := svc.List(context.Background(), "", 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Record ---

func TestRecord_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Record(context.Background(), store.CreateDeployParams{Service: "api", CommitHash: "abc"})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestRecord_MissingService(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.Record(context.Background(), store.CreateDeployParams{CommitHash: "abc"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestRecord_MissingCommit(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.Record(context.Background(), store.CreateDeployParams{Service: "api"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestRecord_HappyPath(t *testing.T) {
	d := &store.Deploy{ID: 1, Service: "api", CommitHash: "abc123"}
	f := &fake{deploy: d}
	svc := NewService(f)
	result, err := svc.Record(context.Background(), store.CreateDeployParams{
		Service:    "api",
		CommitHash: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CommitHash != "abc123" {
		t.Errorf("commit = %q, want abc123", result.CommitHash)
	}
}

// --- GetByCommit ---

func TestGetByCommit_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.GetByCommit(context.Background(), "abc")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestGetByCommit_EmptyHash(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.GetByCommit(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}
