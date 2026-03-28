package code

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fakeEntities implements CodeEntityRepository for testing.
type fakeEntities struct {
	entities []store.CodeEntity
	err      error
}

func (f *fakeEntities) TopByRisk(_ context.Context, _ string, _ int) ([]store.CodeEntity, error) {
	return f.entities, f.err
}

func (f *fakeEntities) GetByName(_ context.Context, _ store.CodeEntityType, _, _ string) (*store.CodeEntity, error) {
	if len(f.entities) > 0 {
		return &f.entities[0], f.err
	}
	return nil, store.ErrNotFound
}

func (f *fakeEntities) BatchGetRisk(_ context.Context, _ store.CodeEntityType, _ []string, _ string) ([]store.CodeEntity, error) {
	return f.entities, f.err
}

// Compile-time check.
var _ CodeEntityRepository = (*fakeEntities)(nil)

// fakeTests implements TestCorrelationRepository for testing.
type fakeTests struct {
	paths []store.UncoveredErrorPath
	err   error
}

func (f *fakeTests) TopByPriority(_ context.Context, _ string, _ int) ([]store.UncoveredErrorPath, error) {
	return f.paths, f.err
}

func (f *fakeTests) GetByFingerprint(_ context.Context, _ string) (*store.UncoveredErrorPath, error) {
	if len(f.paths) > 0 {
		return &f.paths[0], f.err
	}
	return nil, store.ErrNotFound
}

// Compile-time check.
var _ TestCorrelationRepository = (*fakeTests)(nil)

// fakeNotes implements NoteRepository for testing.
type fakeNotes struct {
	note  *store.AgentNote
	notes []store.AgentNote
	err   error
}

func (f *fakeNotes) Upsert(_ context.Context, _, _, _ string) (*store.AgentNote, error) {
	return f.note, f.err
}

func (f *fakeNotes) Get(_ context.Context, _, _ string) (*store.AgentNote, error) {
	return f.note, f.err
}

func (f *fakeNotes) List(_ context.Context, _ string) ([]store.AgentNote, error) {
	return f.notes, f.err
}

// Compile-time check.
var _ NoteRepository = (*fakeNotes)(nil)

// --- Risk ---

func TestRisk_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Risk(context.Background(), "", 10)
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestRisk_DefaultLimit(t *testing.T) {
	svc := NewService(&fakeEntities{}, nil, nil)
	result, err := svc.Risk(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("total = %d, want 0", result.Total)
	}
}

func TestRisk_WithResults(t *testing.T) {
	f := &fakeEntities{
		entities: []store.CodeEntity{
			{EntityName: "users_controller.rb", RiskScore: 85},
			{EntityName: "orders_controller.rb", RiskScore: 72},
		},
	}
	svc := NewService(f, nil, nil)
	result, err := svc.Risk(context.Background(), "api", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("total = %d, want 2", result.Total)
	}
}

// --- Fragile ---

func TestFragile_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Fragile(context.Background(), "", 10)
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestFragile_Success(t *testing.T) {
	f := &fakeTests{
		paths: []store.UncoveredErrorPath{
			{ErrorFingerprint: "abc123"},
		},
	}
	svc := NewService(nil, f, nil)
	result, err := svc.Fragile(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1", result.Total)
	}
}

// --- Annotate ---

func TestAnnotate_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Annotate(context.Background(), "endpoint", "/api/users", "slow")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestAnnotate_EmptyFields(t *testing.T) {
	svc := NewService(nil, nil, &fakeNotes{})
	_, err := svc.Annotate(context.Background(), "", "", "note")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestAnnotate_EmptyNote(t *testing.T) {
	svc := NewService(nil, nil, &fakeNotes{})
	_, err := svc.Annotate(context.Background(), "endpoint", "/api/users", "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestAnnotate_Success(t *testing.T) {
	f := &fakeNotes{note: &store.AgentNote{ID: 1, EntityType: "endpoint", EntityID: "/api/users", Note: "slow"}}
	svc := NewService(nil, nil, f)
	note, err := svc.Annotate(context.Background(), "endpoint", "/api/users", "slow")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if note.Note != "slow" {
		t.Errorf("note = %q, want 'slow'", note.Note)
	}
}
