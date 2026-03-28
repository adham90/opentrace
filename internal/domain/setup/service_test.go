package setup

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fakeLogs implements LogCounter for testing.
type fakeLogs struct {
	levels map[string]int
	err    error
}

func (f *fakeLogs) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return f.levels, f.err
}

// Compile-time check.
var _ LogCounter = (*fakeLogs)(nil)

// fakeUsers implements UserCounter for testing.
type fakeUsers struct {
	count int
	err   error
}

func (f *fakeUsers) Count(_ context.Context) (int, error) {
	return f.count, f.err
}

// Compile-time check.
var _ UserCounter = (*fakeUsers)(nil)

// fakeSources implements DataSourceLister for testing.
type fakeSources struct {
	sources []store.DataSource
	err     error
}

func (f *fakeSources) List(_ context.Context, _ store.ListDataSourceParams) ([]store.DataSource, error) {
	return f.sources, f.err
}

// Compile-time check.
var _ DataSourceLister = (*fakeSources)(nil)

// --- Status ---

func TestStatus_AllNil(t *testing.T) {
	svc := NewService(nil, nil, nil)
	result, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasUsers || result.HasLogs || result.HasDataSources {
		t.Error("expected all false for nil deps")
	}
	if result.IsComplete {
		t.Error("expected IsComplete=false")
	}
}

func TestStatus_Complete(t *testing.T) {
	svc := NewService(
		&fakeLogs{levels: map[string]int{"INFO": 100}},
		&fakeUsers{count: 1},
		&fakeSources{sources: []store.DataSource{{Name: "db"}}},
	)
	result, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasUsers {
		t.Error("expected HasUsers=true")
	}
	if !result.HasLogs {
		t.Error("expected HasLogs=true")
	}
	if !result.HasDataSources {
		t.Error("expected HasDataSources=true")
	}
	if !result.IsComplete {
		t.Error("expected IsComplete=true")
	}
}

func TestStatus_UserCountError(t *testing.T) {
	svc := NewService(nil, &fakeUsers{err: errors.New("db down")}, nil)
	_, err := svc.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Verify ---

func TestVerify_AllNil(t *testing.T) {
	svc := NewService(nil, nil, nil)
	err := svc.Verify()
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestVerify_WithDeps(t *testing.T) {
	svc := NewService(&fakeLogs{}, nil, nil)
	err := svc.Verify()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Guide ---

func TestGuide_NothingConfigured(t *testing.T) {
	svc := NewService(
		&fakeLogs{levels: map[string]int{}},
		&fakeUsers{count: 0},
		&fakeSources{},
	)
	steps, err := svc.Guide(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Errorf("steps = %d, want 3", len(steps))
	}
}

func TestGuide_AllConfigured(t *testing.T) {
	svc := NewService(
		&fakeLogs{levels: map[string]int{"INFO": 1}},
		&fakeUsers{count: 1},
		&fakeSources{sources: []store.DataSource{{Name: "db"}}},
	)
	steps, err := svc.Guide(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Errorf("steps = %d, want 1 (complete message)", len(steps))
	}
}
