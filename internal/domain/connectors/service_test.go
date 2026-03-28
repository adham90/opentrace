package connectors

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fakeRepo implements DataSourceRepository for testing.
type fakeRepo struct {
	sources []store.DataSource
	source  *store.DataSource
	err     error
}

func (f *fakeRepo) List(_ context.Context, _ store.ListDataSourceParams) ([]store.DataSource, error) {
	return f.sources, f.err
}

func (f *fakeRepo) GetByID(_ context.Context, _ uuid.UUID) (*store.DataSource, error) {
	return f.source, f.err
}

func (f *fakeRepo) Create(_ context.Context, _ store.CreateDataSourceParams) (*store.DataSource, error) {
	return f.source, f.err
}

func (f *fakeRepo) Update(_ context.Context, _ uuid.UUID, _ store.UpdateDataSourceParams) (*store.DataSource, error) {
	return f.source, f.err
}

func (f *fakeRepo) Delete(_ context.Context, _ uuid.UUID) error {
	return f.err
}

// Compile-time check.
var _ DataSourceRepository = (*fakeRepo)(nil)

// --- List ---

func TestList_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.List(context.Background(), store.ListDataSourceParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestList_Success(t *testing.T) {
	f := &fakeRepo{sources: []store.DataSource{{Name: "prod-db"}}}
	svc := NewService(f)
	sources, err := svc.List(context.Background(), store.ListDataSourceParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 1 {
		t.Errorf("len = %d, want 1", len(sources))
	}
}

// --- Get ---

func TestGet_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestGet_NilID(t *testing.T) {
	svc := NewService(&fakeRepo{})
	_, err := svc.Get(context.Background(), uuid.Nil)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestGet_Success(t *testing.T) {
	id := uuid.New()
	f := &fakeRepo{source: &store.DataSource{ID: id, Name: "prod-db"}}
	svc := NewService(f)
	ds, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ds.Name != "prod-db" {
		t.Errorf("name = %q, want 'prod-db'", ds.Name)
	}
}

// --- Create ---

func TestCreate_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Create(context.Background(), store.CreateDataSourceParams{Name: "x", Type: "postgres"})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestCreate_MissingName(t *testing.T) {
	svc := NewService(&fakeRepo{})
	_, err := svc.Create(context.Background(), store.CreateDataSourceParams{Type: "postgres"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreate_MissingType(t *testing.T) {
	svc := NewService(&fakeRepo{})
	_, err := svc.Create(context.Background(), store.CreateDataSourceParams{Name: "prod-db"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

// --- Delete ---

func TestDelete_NilID(t *testing.T) {
	svc := NewService(&fakeRepo{})
	err := svc.Delete(context.Background(), uuid.Nil)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}
