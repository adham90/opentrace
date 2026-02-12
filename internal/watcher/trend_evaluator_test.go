package watcher

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

type mockRunStoreForTrend struct {
	runs []store.WatcherRun
}

func (m *mockRunStoreForTrend) List(_ context.Context, _ uuid.UUID, _ int) ([]store.WatcherRun, error) {
	return m.runs, nil
}

func (m *mockRunStoreForTrend) Create(_ context.Context, _ uuid.UUID) (*store.WatcherRun, error) { return nil, nil }
func (m *mockRunStoreForTrend) Complete(_ context.Context, _ uuid.UUID, _ string, _ any, _ bool) error { return nil }
func (m *mockRunStoreForTrend) Fail(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockRunStoreForTrend) FailStaleRuns(_ context.Context, _ time.Duration) (int, error) { return 0, nil }
func (m *mockRunStoreForTrend) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockRunStoreForTrend) CountRuns(_ context.Context, _ store.CountRunParams) (int, error) { return 0, nil }
func (m *mockRunStoreForTrend) ListRecentFailed(_ context.Context, _ int) ([]store.WatcherRun, error) { return nil, nil }
func (m *mockRunStoreForTrend) ListWithFilter(_ context.Context, _ uuid.UUID, _ int, _ string) ([]store.WatcherRun, error) { return nil, nil }
func (m *mockRunStoreForTrend) GetByID(_ context.Context, _ uuid.UUID) (*store.WatcherRun, error) { return nil, store.ErrNotFound }

type mockLogStoreForTrend struct {
	entries []store.LogEntry
}

func (m *mockLogStoreForTrend) Search(_ context.Context, _ store.LogSearchParams) ([]store.LogEntry, error) {
	return m.entries, nil
}

func (m *mockLogStoreForTrend) BatchInsert(_ context.Context, _ []store.LogEntry) (int, error) { return 0, nil }
func (m *mockLogStoreForTrend) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockLogStoreForTrend) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) { return nil, nil }
func (m *mockLogStoreForTrend) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) { return nil, nil }
func (m *mockLogStoreForTrend) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) { return nil, nil }
func (m *mockLogStoreForTrend) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) { return nil, nil }
func (m *mockLogStoreForTrend) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) { return nil, store.ErrNotFound }

func makeTrendRuns(values ...float64) []store.WatcherRun {
	var runs []store.WatcherRun
	for _, v := range values {
		snap, _ := json.Marshal(trendSnapshot{Value: v})
		runs = append(runs, store.WatcherRun{
			ID:      uuid.New(),
			Status:  "completed",
			Details: snap,
		})
	}
	return runs
}

func makeTrendWatcher(cfg TrendConfig) store.Watcher {
	tc, _ := json.Marshal(cfg)
	return store.Watcher{
		ID:          uuid.New(),
		Title:       "Trend Test",
		WatcherType: store.WatcherTypeTrend,
		TypeConfig:  tc,
	}
}

func TestTrend_InsufficientBaseline(t *testing.T) {
	ls := &mockLogStoreForTrend{
		entries: make([]store.LogEntry, 10),
	}
	rs := &mockRunStoreForTrend{
		runs: makeTrendRuns(5.0), // only 1 run, need 3
	}
	te := NewTrendEvaluator(nil, ls, rs)

	w := makeTrendWatcher(TrendConfig{
		Source:        "logs",
		BaselineRuns:  3,
		ChangePercent: 50,
		Direction:     "both",
		Window:        "1h",
	})

	result, err := te.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when baseline is insufficient")
	}
}

func TestTrend_StableNoAlert(t *testing.T) {
	ls := &mockLogStoreForTrend{
		entries: make([]store.LogEntry, 100), // current = 100
	}
	rs := &mockRunStoreForTrend{
		runs: makeTrendRuns(95.0, 105.0, 100.0), // avg = 100
	}
	te := NewTrendEvaluator(nil, ls, rs)

	w := makeTrendWatcher(TrendConfig{
		Source:        "logs",
		BaselineRuns:  3,
		ChangePercent: 50,
		Direction:     "both",
		Window:        "1h",
	})

	result, err := te.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when value is stable")
	}
}

func TestTrend_IncreaseAboveThreshold(t *testing.T) {
	ls := &mockLogStoreForTrend{
		entries: make([]store.LogEntry, 200), // current = 200
	}
	rs := &mockRunStoreForTrend{
		runs: makeTrendRuns(100.0, 100.0, 100.0), // avg = 100
	}
	te := NewTrendEvaluator(nil, ls, rs)

	w := makeTrendWatcher(TrendConfig{
		Source:        "logs",
		BaselineRuns:  3,
		ChangePercent: 50,
		Direction:     "increase",
		Window:        "1h",
	})

	result, err := te.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when value increased above threshold")
	}
}

func TestTrend_DecreaseAboveThreshold(t *testing.T) {
	ls := &mockLogStoreForTrend{
		entries: make([]store.LogEntry, 30), // current = 30
	}
	rs := &mockRunStoreForTrend{
		runs: makeTrendRuns(100.0, 100.0, 100.0), // avg = 100
	}
	te := NewTrendEvaluator(nil, ls, rs)

	w := makeTrendWatcher(TrendConfig{
		Source:        "logs",
		BaselineRuns:  3,
		ChangePercent: 50,
		Direction:     "decrease",
		Window:        "1h",
	})

	result, err := te.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when value decreased above threshold")
	}
}

func TestTrend_DirectionFiltering(t *testing.T) {
	// Value increased, but direction is "decrease" — should not trigger
	ls := &mockLogStoreForTrend{
		entries: make([]store.LogEntry, 200), // current = 200
	}
	rs := &mockRunStoreForTrend{
		runs: makeTrendRuns(100.0, 100.0, 100.0), // avg = 100
	}
	te := NewTrendEvaluator(nil, ls, rs)

	w := makeTrendWatcher(TrendConfig{
		Source:        "logs",
		BaselineRuns:  3,
		ChangePercent: 50,
		Direction:     "decrease", // only alert on decrease
		Window:        "1h",
	})

	result, err := te.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when change direction doesn't match")
	}
}

func TestTrend_MissingConfig(t *testing.T) {
	te := NewTrendEvaluator(nil, nil, nil)
	w := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeTrend,
	}

	_, err := te.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for missing type_config")
	}
}
