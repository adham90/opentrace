package watcher

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

type mockLogStoreForSequence struct {
	entries []store.LogEntry
}

func (m *mockLogStoreForSequence) Search(_ context.Context, params store.LogSearchParams) ([]store.LogEntry, error) {
	var result []store.LogEntry
	for _, e := range m.entries {
		if params.Start != nil && e.Timestamp.Before(*params.Start) {
			continue
		}
		if params.Level != "" && e.Level != params.Level {
			continue
		}
		if params.Service != "" && e.Service != params.Service {
			continue
		}
		result = append(result, e)
		if params.Limit > 0 && len(result) >= params.Limit {
			break
		}
	}
	return result, nil
}

func (m *mockLogStoreForSequence) BatchInsert(_ context.Context, _ []store.LogEntry) (int, error) { return 0, nil }
func (m *mockLogStoreForSequence) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockLogStoreForSequence) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) { return nil, nil }
func (m *mockLogStoreForSequence) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) { return nil, nil }
func (m *mockLogStoreForSequence) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) { return nil, nil }
func (m *mockLogStoreForSequence) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) { return nil, nil }
func (m *mockLogStoreForSequence) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) { return nil, store.ErrNotFound }
func (m *mockLogStoreForSequence) RecordBatch(_ context.Context, _ string, _ int) error { return nil }
func (m *mockLogStoreForSequence) GetBatch(_ context.Context, _ string) (*store.BatchRecord, error) { return nil, nil }
func (m *mockLogStoreForSequence) PruneBatches(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

type mockAlertStoreForSequence struct {
	alerts []store.Alert
}

func (m *mockAlertStoreForSequence) List(_ context.Context, params store.ListAlertParams) ([]store.Alert, error) {
	var result []store.Alert
	for _, a := range m.alerts {
		if params.WatcherID != nil && (a.WatcherID == nil || *a.WatcherID != *params.WatcherID) {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (m *mockAlertStoreForSequence) Create(_ context.Context, _ store.CreateAlertParams) (*store.Alert, error) { return nil, nil }
func (m *mockAlertStoreForSequence) CountUnread(_ context.Context) (int, error) { return 0, nil }
func (m *mockAlertStoreForSequence) MarkRead(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockAlertStoreForSequence) Dismiss(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockAlertStoreForSequence) Snooze(_ context.Context, _ uuid.UUID, _ time.Time) error { return nil }
func (m *mockAlertStoreForSequence) Unsnooze(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockAlertStoreForSequence) WatcherAlertStats(_ context.Context, _ uuid.UUID, _ time.Time) (*store.WatcherEffectiveness, error) { return nil, nil }
func (m *mockAlertStoreForSequence) DismissAll(_ context.Context) error { return nil }
func (m *mockAlertStoreForSequence) CountTotal(_ context.Context) (int, error) { return 0, nil }
func (m *mockAlertStoreForSequence) MarkAllRead(_ context.Context) error { return nil }
func (m *mockAlertStoreForSequence) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockAlertStoreForSequence) CountBySeverity(_ context.Context, _, _ time.Time) (map[string]int, error) { return nil, nil }
func (m *mockAlertStoreForSequence) GetByID(_ context.Context, _ uuid.UUID) (*store.Alert, error) { return nil, store.ErrNotFound }

func makeSequenceWatcher(cfg SequenceConfig) store.Watcher {
	tc, _ := json.Marshal(cfg)
	return store.Watcher{
		ID:          uuid.New(),
		Title:       "Sequence Test",
		WatcherType: store.WatcherTypeSequence,
		TypeConfig:  tc,
	}
}

func TestSequence_AllStepsMatched(t *testing.T) {
	now := time.Now()
	ls := &mockLogStoreForSequence{
		entries: []store.LogEntry{
			{Timestamp: now.Add(-30 * time.Minute), Level: "error", Service: "auth"},
			{Timestamp: now.Add(-15 * time.Minute), Level: "error", Service: "api"},
		},
	}
	se := NewSequenceEvaluator(ls, nil)

	w := makeSequenceWatcher(SequenceConfig{
		Window: "1h",
		Steps: []SequenceStep{
			{Source: "logs", Filter: &LogFilter{Level: "error", Service: "auth"}, Label: "auth_error"},
			{Source: "logs", Filter: &LogFilter{Level: "error", Service: "api"}, Label: "api_error"},
		},
	})

	result, err := se.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when all steps match in order")
	}
}

func TestSequence_MiddleStepFails(t *testing.T) {
	now := time.Now()
	ls := &mockLogStoreForSequence{
		entries: []store.LogEntry{
			{Timestamp: now.Add(-30 * time.Minute), Level: "error", Service: "auth"},
			// No api errors — middle step fails
			{Timestamp: now.Add(-5 * time.Minute), Level: "error", Service: "db"},
		},
	}
	se := NewSequenceEvaluator(ls, nil)

	w := makeSequenceWatcher(SequenceConfig{
		Window: "1h",
		Steps: []SequenceStep{
			{Source: "logs", Filter: &LogFilter{Level: "error", Service: "auth"}},
			{Source: "logs", Filter: &LogFilter{Level: "error", Service: "api"}}, // won't match
			{Source: "logs", Filter: &LogFilter{Level: "error", Service: "db"}},
		},
	})

	result, err := se.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when middle step fails")
	}
}

func TestSequence_AlertSourceMatched(t *testing.T) {
	now := time.Now()
	watcherID := uuid.New()

	ls := &mockLogStoreForSequence{
		entries: []store.LogEntry{
			{Timestamp: now.Add(-30 * time.Minute), Level: "error", Service: "auth"},
		},
	}
	as := &mockAlertStoreForSequence{
		alerts: []store.Alert{
			{WatcherID: &watcherID, CreatedAt: now.Add(-10 * time.Minute)},
		},
	}
	se := NewSequenceEvaluator(ls, as)

	w := makeSequenceWatcher(SequenceConfig{
		Window: "1h",
		Steps: []SequenceStep{
			{Source: "logs", Filter: &LogFilter{Level: "error", Service: "auth"}},
			{Source: "alert", WatcherID: watcherID.String()},
		},
	})

	result, err := se.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when log + alert steps both match")
	}
}

func TestSequence_AlertSourceUnmatched(t *testing.T) {
	now := time.Now()
	watcherID := uuid.New()

	ls := &mockLogStoreForSequence{
		entries: []store.LogEntry{
			{Timestamp: now.Add(-30 * time.Minute), Level: "error", Service: "auth"},
		},
	}
	as := &mockAlertStoreForSequence{
		alerts: nil, // no alerts
	}
	se := NewSequenceEvaluator(ls, as)

	w := makeSequenceWatcher(SequenceConfig{
		Window: "1h",
		Steps: []SequenceStep{
			{Source: "logs", Filter: &LogFilter{Level: "error", Service: "auth"}},
			{Source: "alert", WatcherID: watcherID.String()},
		},
	})

	result, err := se.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when alert step doesn't match")
	}
}

func TestSequence_EmptySteps(t *testing.T) {
	se := NewSequenceEvaluator(nil, nil)
	w := makeSequenceWatcher(SequenceConfig{
		Window: "1h",
		Steps:  nil,
	})

	result, err := se.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert for empty steps")
	}
}

func TestSequence_SingleStep(t *testing.T) {
	now := time.Now()
	ls := &mockLogStoreForSequence{
		entries: []store.LogEntry{
			{Timestamp: now.Add(-10 * time.Minute), Level: "error"},
		},
	}
	se := NewSequenceEvaluator(ls, nil)

	w := makeSequenceWatcher(SequenceConfig{
		Window: "1h",
		Steps: []SequenceStep{
			{Source: "logs", Filter: &LogFilter{Level: "error"}},
		},
	})

	result, err := se.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert for single matching step")
	}
}

func TestSequence_MissingConfig(t *testing.T) {
	se := NewSequenceEvaluator(nil, nil)
	w := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeSequence,
	}

	_, err := se.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for missing type_config")
	}
}
