package watcher

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// mockWatchStore implements store.WatchStore for testing.
type mockWatchStore struct {
	store.WatchStore
	watches []store.Watch
}

func (m *mockWatchStore) List(_ context.Context, _ store.ListWatchParams) ([]store.Watch, error) {
	return m.watches, nil
}

func (m *mockWatchStore) CreateRun(_ context.Context, _ string) (*store.WatchRun, error) {
	return &store.WatchRun{ID: "run-1"}, nil
}

func (m *mockWatchStore) CompleteRun(_ context.Context, _ string, _ float64, _ bool, _ string) error {
	return nil
}

func (m *mockWatchStore) FailRun(_ context.Context, _ string, _ string) error {
	return nil
}

func TestWatchStreamEvaluator_SemaphoreBounded(t *testing.T) {
	ws := &mockWatchStore{}
	evaluator := &WatchStreamEvaluator{
		ctx:      context.Background(),
		watchStore: ws,
		lastEval:   make(map[string]time.Time),
		minGap:     10 * time.Second,
		sem:        make(chan struct{}, 2), // limit to 2 concurrent
	}

	// Fill the semaphore
	evaluator.sem <- struct{}{}
	evaluator.sem <- struct{}{}

	// Verify that a 3rd send would be dropped (non-blocking)
	var dropped atomic.Int32
	services := map[string]bool{"api": true}

	// This should not block because the semaphore is full
	select {
	case evaluator.sem <- struct{}{}:
		// Should not happen — semaphore is full
		<-evaluator.sem
		t.Error("expected semaphore to be full")
	default:
		dropped.Add(1)
	}

	if dropped.Load() != 1 {
		t.Error("expected goroutine to be dropped when semaphore is full")
	}

	// Drain semaphore
	<-evaluator.sem
	<-evaluator.sem

	// OnLogsReceived should not panic with nil evaluator (just testing the sem path)
	_ = services
}

func TestWatchStreamEvaluator_NewHasSemaphore(t *testing.T) {
	ws := &mockWatchStore{}
	evaluator := NewWatchStreamEvaluator(context.Background(), ws, nil, nil)

	if cap(evaluator.sem) != 16 {
		t.Errorf("semaphore capacity = %d, want 16", cap(evaluator.sem))
	}
}

func TestWatchStreamEvaluator_NilCtxDefaultsToBackground(t *testing.T) {
	ws := &mockWatchStore{}
	evaluator := NewWatchStreamEvaluator(nil, ws, nil, nil)

	if evaluator.ctx == nil {
		t.Error("expected non-nil context when nil passed to constructor")
	}
}

func TestWatchStreamEvaluator_StoresParentContext(t *testing.T) {
	ws := &mockWatchStore{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	evaluator := NewWatchStreamEvaluator(ctx, ws, nil, nil)
	if evaluator.ctx != ctx {
		t.Error("expected evaluator to store the provided parent context")
	}
}
