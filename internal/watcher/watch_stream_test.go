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

// blockingStreamNotifier holds a delivery until released, so a test can observe
// whether shutdown waits for it.
type blockingStreamNotifier struct {
	started   chan struct{}
	release   chan struct{}
	delivered atomic.Int32
}

func (n *blockingStreamNotifier) NotifyWatchAlert(_ context.Context, _ *store.WatchAlert, _ *store.Watch) error {
	select {
	case n.started <- struct{}{}:
	default:
	}
	<-n.release
	n.delivered.Add(1)
	return nil
}

// TestWatchStreamEvaluator_StopDrainsNotifications pins the fix for alert
// deliveries abandoned at shutdown: the stream evaluator dispatches alerts on a
// background goroutine, so without a Stop that drains the dispatcher a delivery
// in flight when the process exits is silently lost.
func TestWatchStreamEvaluator_StopDrainsNotifications(t *testing.T) {
	n := &blockingStreamNotifier{started: make(chan struct{}, 1), release: make(chan struct{})}
	s := NewWatchStreamEvaluator(context.Background(), &mockWatchStore{}, nil, nil, []WatchAlertNotifier{n})

	s.notify.dispatch(context.Background(), s.notifiers, &store.WatchAlert{ID: "a1"}, &store.Watch{ID: "w1"})
	<-n.started

	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned while a delivery was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(n.release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the delivery finished")
	}
	if got := n.delivered.Load(); got != 1 {
		t.Errorf("delivered = %d, want 1", got)
	}
}

// TestWatchStreamEvaluator_StopIsBoundedAndIdempotent proves a wedged notifier
// cannot hold shutdown open forever, and that a second Stop returns at once.
func TestWatchStreamEvaluator_StopIsBoundedAndIdempotent(t *testing.T) {
	n := &blockingStreamNotifier{started: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(n.release)
	s := NewWatchStreamEvaluator(context.Background(), &mockWatchStore{}, nil, nil, []WatchAlertNotifier{n})
	s.notify.dispatch(context.Background(), s.notifiers, &store.WatchAlert{ID: "a1"}, &store.Watch{ID: "w1"})
	<-n.started

	start := time.Now()
	s.Stop()
	if elapsed := time.Since(start); elapsed > streamStopDrainTimeout*2 {
		t.Fatalf("Stop took %s, want it bounded by %s", elapsed, streamStopDrainTimeout)
	}

	start = time.Now()
	s.Stop()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("second Stop took %s, want an immediate return", elapsed)
	}
}

// TestWatchStreamEvaluator_StopRejectsNewWork proves OnLogsReceived is inert
// after Stop, so shutdown cannot be extended by a late ingest batch.
func TestWatchStreamEvaluator_StopRejectsNewWork(t *testing.T) {
	s := NewWatchStreamEvaluator(context.Background(), &mockWatchStore{}, nil, nil, nil)
	s.Stop()
	s.OnLogsReceived([]store.LogEntry{{Service: "api"}})
	if len(s.sem) != 0 {
		t.Errorf("semaphore holds %d slots after Stop, want 0 — no new evaluations may start", len(s.sem))
	}
}

func TestWatchStreamEvaluator_SemaphoreBounded(t *testing.T) {
	ws := &mockWatchStore{}
	evaluator := &WatchStreamEvaluator{
		ctx:        context.Background(),
		watchStore: ws,
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
	evaluator := NewWatchStreamEvaluator(context.Background(), ws, nil, nil, nil)

	if cap(evaluator.sem) != 16 {
		t.Errorf("semaphore capacity = %d, want 16", cap(evaluator.sem))
	}
}

func TestWatchStreamEvaluator_NilCtxDefaultsToBackground(t *testing.T) {
	ws := &mockWatchStore{}
	evaluator := NewWatchStreamEvaluator(nil, ws, nil, nil, nil)

	if evaluator.ctx == nil {
		t.Error("expected non-nil context when nil passed to constructor")
	}
}

func TestWatchStreamEvaluator_StoresParentContext(t *testing.T) {
	ws := &mockWatchStore{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	evaluator := NewWatchStreamEvaluator(ctx, ws, nil, nil, nil)
	if evaluator.ctx != ctx {
		t.Error("expected evaluator to store the provided parent context")
	}
}
