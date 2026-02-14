package watcher

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

type mockLogStoreForDeadman struct {
	entries []store.LogEntry
	err     error
}

func (m *mockLogStoreForDeadman) Search(_ context.Context, _ store.LogSearchParams) ([]store.LogEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

func (m *mockLogStoreForDeadman) BatchInsert(_ context.Context, _ []store.LogEntry) (int, error) { return 0, nil }
func (m *mockLogStoreForDeadman) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockLogStoreForDeadman) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) { return nil, nil }
func (m *mockLogStoreForDeadman) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) { return nil, nil }
func (m *mockLogStoreForDeadman) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) { return nil, nil }
func (m *mockLogStoreForDeadman) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) { return nil, nil }
func (m *mockLogStoreForDeadman) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) { return nil, store.ErrNotFound }
func (m *mockLogStoreForDeadman) RecordBatch(_ context.Context, _ string, _ int) error { return nil }
func (m *mockLogStoreForDeadman) GetBatch(_ context.Context, _ string) (*store.BatchRecord, error) { return nil, nil }
func (m *mockLogStoreForDeadman) PruneBatches(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockLogStoreForDeadman) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) { return nil, nil }

func makeDeadmanWatcher(cfg DeadmanConfig) store.Watcher {
	tc, _ := json.Marshal(cfg)
	return store.Watcher{
		ID:          uuid.New(),
		Title:       "Deadman Test",
		WatcherType: store.WatcherTypeDeadman,
		TypeConfig:  tc,
	}
}

func TestDeadmanEvaluator_LogsEnoughEvents(t *testing.T) {
	ls := &mockLogStoreForDeadman{
		entries: []store.LogEntry{{}, {}, {}}, // 3 events
	}
	de := NewDeadmanEvaluator(nil, ls)
	w := makeDeadmanWatcher(DeadmanConfig{Source: "logs", MinCount: 2, Window: "30m"})

	result, err := de.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when enough events found")
	}
}

func TestDeadmanEvaluator_LogsTooFewEvents(t *testing.T) {
	ls := &mockLogStoreForDeadman{
		entries: []store.LogEntry{{}}, // 1 event
	}
	de := NewDeadmanEvaluator(nil, ls)
	w := makeDeadmanWatcher(DeadmanConfig{Source: "logs", MinCount: 5, Window: "30m"})

	result, err := de.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when too few events")
	}
}

func TestDeadmanEvaluator_LogsZeroEvents(t *testing.T) {
	ls := &mockLogStoreForDeadman{
		entries: nil,
	}
	de := NewDeadmanEvaluator(nil, ls)
	w := makeDeadmanWatcher(DeadmanConfig{Source: "logs", MinCount: 1, Window: "15m"})

	result, err := de.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when zero events found")
	}
}

func TestDeadmanEvaluator_LogsWithFilter(t *testing.T) {
	ls := &mockLogStoreForDeadman{
		entries: []store.LogEntry{{}, {}},
	}
	de := NewDeadmanEvaluator(nil, ls)
	w := makeDeadmanWatcher(DeadmanConfig{
		Source:   "logs",
		MinCount: 1,
		Window:   "1h",
		Filter:   &LogFilter{Level: "error", Service: "api"},
	})

	result, err := de.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when filter matches enough events")
	}
}

func TestDeadmanEvaluator_MissingConfig(t *testing.T) {
	de := NewDeadmanEvaluator(nil, nil)
	w := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeDeadman,
	}

	_, err := de.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for missing type_config")
	}
}

func TestDeadmanEvaluator_InvalidJSON(t *testing.T) {
	de := NewDeadmanEvaluator(nil, nil)
	w := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeDeadman,
		TypeConfig:  json.RawMessage(`{invalid`),
	}

	_, err := de.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDeadmanEvaluator_UnknownSource(t *testing.T) {
	de := NewDeadmanEvaluator(nil, nil)
	w := makeDeadmanWatcher(DeadmanConfig{Source: "unknown", MinCount: 1, Window: "5m"})

	_, err := de.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for unknown source")
	}
}
