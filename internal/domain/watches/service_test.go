package watches

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fake implements Repository for testing.
type fake struct {
	watches  []store.Watch
	watch    *store.Watch
	alerts   []store.WatchAlert
	alert    *store.WatchAlert
	pending  int
	err      error
	alertErr error
}

func (f *fake) List(_ context.Context, _ store.ListWatchParams) ([]store.Watch, error) {
	return f.watches, f.err
}

func (f *fake) GetByID(_ context.Context, _ string) (*store.Watch, error) {
	return f.watch, f.err
}

func (f *fake) Create(_ context.Context, _ store.CreateWatchParams) (*store.Watch, error) {
	return f.watch, f.err
}

func (f *fake) Delete(_ context.Context, _ string) error {
	return f.err
}

func (f *fake) GetAlert(_ context.Context, _ string) (*store.WatchAlert, error) {
	return f.alert, f.alertErr
}

func (f *fake) ListAlerts(_ context.Context, _ string, _ string, _ int) ([]store.WatchAlert, error) {
	return f.alerts, f.alertErr
}

func (f *fake) CountPendingAlerts(_ context.Context) (int, error) {
	return f.pending, f.alertErr
}

func (f *fake) DismissAlert(_ context.Context, _ string, _ string) error {
	return f.alertErr
}

func (f *fake) AcknowledgeAlert(_ context.Context, _ string) error {
	return f.alertErr
}

var _ Repository = (*fake)(nil)

// --- List ---

func TestList_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.List(context.Background(), store.ListWatchParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestList_HappyPath(t *testing.T) {
	f := &fake{
		watches: []store.Watch{
			{ID: "w1", ConditionsJSON: json.RawMessage(`{"type":"threshold","metric":"error_rate","op":"gt","value":0.05}`)},
			{ID: "w2", ConditionsJSON: json.RawMessage(`{"type":"threshold","metric":"response_time","op":"gt","value":500}`)},
		},
	}
	svc := NewService(f)
	result, err := svc.List(context.Background(), store.ListWatchParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d watches, want 2", len(result))
	}
}

// --- Create ---

func TestCreate_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Create(context.Background(), store.CreateWatchParams{
		ConditionsJSON: json.RawMessage(`{"type":"threshold","metric":"error_rate","op":"gt","value":0.05}`),
	})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestCreate_MissingConditions(t *testing.T) {
	svc := NewService(&fake{})
	_, err := svc.Create(context.Background(), store.CreateWatchParams{})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreate_HappyPath(t *testing.T) {
	cond := json.RawMessage(`{"type":"threshold","metric":"error_rate","op":"gt","value":0.05}`)
	w := &store.Watch{ID: "w1", ConditionsJSON: cond}
	f := &fake{watch: w}
	svc := NewService(f)
	result, err := svc.Create(context.Background(), store.CreateWatchParams{ConditionsJSON: cond})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "w1" {
		t.Errorf("id = %q, want w1", result.ID)
	}
}

// --- Delete ---

func TestDelete_NilRepo(t *testing.T) {
	svc := &Service{}
	err := svc.Delete(context.Background(), "w1")
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

// --- Alerts ---

func TestAlerts_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Alerts(context.Background(), "", "", 10)
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestAlerts_HappyPath(t *testing.T) {
	f := &fake{
		alerts: []store.WatchAlert{
			{ID: "a1", WatchID: "w1", Summary: "error spike"},
		},
	}
	svc := NewService(f)
	result, err := svc.Alerts(context.Background(), "w1", "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("got %d alerts, want 1", len(result))
	}
}

// --- Dismiss ---

func TestDismiss_NilRepo(t *testing.T) {
	svc := &Service{}
	err := svc.Dismiss(context.Background(), "a1", "false positive")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestDismiss_EmptyID(t *testing.T) {
	svc := NewService(&fake{})
	err := svc.Dismiss(context.Background(), "", "reason")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

// --- Acknowledge ---

func TestAcknowledge_NilRepo(t *testing.T) {
	svc := &Service{}
	err := svc.Acknowledge(context.Background(), "a1")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestAcknowledge_EmptyID(t *testing.T) {
	svc := NewService(&fake{})
	err := svc.Acknowledge(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}
