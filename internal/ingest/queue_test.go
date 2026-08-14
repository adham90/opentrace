package ingest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Mock LogStore for queue tests
// ---------------------------------------------------------------------------

// queueMockLogStore records calls to BatchInsert for assertions.
type queueMockLogStore struct {
	mu          sync.Mutex
	calls       [][]store.LogEntry
	totalCount  atomic.Int64
	batchErr    error
	insertDelay time.Duration // optional delay to simulate slow inserts
}

func (m *queueMockLogStore) BatchInsert(_ context.Context, entries []store.LogEntry) (int, error) {
	if m.insertDelay > 0 {
		time.Sleep(m.insertDelay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, entries)
	m.totalCount.Add(int64(len(entries)))
	if m.batchErr != nil {
		return 0, m.batchErr
	}
	return len(entries), nil
}

func (m *queueMockLogStore) totalInserted() int64 {
	return m.totalCount.Load()
}

func (m *queueMockLogStore) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// Unused interface methods.
func (m *queueMockLogStore) Search(context.Context, store.LogSearchParams) ([]store.LogEntry, error) {
	return nil, nil
}
func (m *queueMockLogStore) Prune(context.Context, time.Duration) (int64, error) { return 0, nil }
func (m *queueMockLogStore) CountByLevel(context.Context, store.LogCountParams) (map[string]int, error) {
	return nil, nil
}
func (m *queueMockLogStore) CountByService(context.Context, store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}
func (m *queueMockLogStore) Histogram(context.Context, store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}
func (m *queueMockLogStore) DistinctValues(context.Context, string, store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (m *queueMockLogStore) MetadataKeys(context.Context, store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (m *queueMockLogStore) GetByID(context.Context, int64) (*store.LogEntry, error) { return nil, nil }
func (m *queueMockLogStore) SearchRequestSummaries(context.Context, store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}
func (m *queueMockLogStore) AggregateRequestSummaries(context.Context, store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return &store.RequestSummaryAggregates{}, nil
}
func (m *queueMockLogStore) RecordBatch(context.Context, string, int) error { return nil }
func (m *queueMockLogStore) GetBatch(context.Context, string) (*store.BatchRecord, error) {
	return nil, store.ErrNotFound
}
func (m *queueMockLogStore) PruneBatches(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeEntries(n int) []store.LogEntry {
	entries := make([]store.LogEntry, n)
	for i := range entries {
		entries[i] = store.LogEntry{
			Timestamp: time.Now().UTC(),
			Level:     "info",
			Service:   "test-svc",
			Message:   "queue test entry",
		}
	}
	return entries
}

// ---------------------------------------------------------------------------
// Tests: NewQueue
// ---------------------------------------------------------------------------

func TestNewQueue_Defaults(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{})
	defer q.Stop()

	if q.maxQueueSize != 10000 {
		t.Errorf("expected default maxQueueSize=10000, got %d", q.maxQueueSize)
	}
	if q.maxBatchSize != 1000 {
		t.Errorf("expected default maxBatchSize=1000, got %d", q.maxBatchSize)
	}
	if q.flushInterval != 100*time.Millisecond {
		t.Errorf("expected default flushInterval=100ms, got %v", q.flushInterval)
	}
}

func TestNewQueue_CustomConfig(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  500,
		MaxBatchSize:  50,
		FlushInterval: 200 * time.Millisecond,
	})
	defer q.Stop()

	if q.maxQueueSize != 500 {
		t.Errorf("expected maxQueueSize=500, got %d", q.maxQueueSize)
	}
	if q.maxBatchSize != 50 {
		t.Errorf("expected maxBatchSize=50, got %d", q.maxBatchSize)
	}
	if q.flushInterval != 200*time.Millisecond {
		t.Errorf("expected flushInterval=200ms, got %v", q.flushInterval)
	}
}

// ---------------------------------------------------------------------------
// Tests: Enqueue
// ---------------------------------------------------------------------------

func TestEnqueue_WithinCapacity(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second, // large interval so timer doesn't fire
	})
	defer q.Stop()

	entries := makeEntries(5)
	count, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 5 {
		t.Errorf("expected count=5, got %d", count)
	}
	if q.QueueDepth() != 5 {
		t.Errorf("expected queue depth=5, got %d", q.QueueDepth())
	}
	// Nothing should have been flushed to the store yet (below batch size)
	if ms.totalInserted() != 0 {
		t.Errorf("expected 0 inserted (buffered only), got %d", ms.totalInserted())
	}
}

func TestEnqueue_TriggersBatchFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  200,
		MaxBatchSize:  10, // small batch size to trigger flush
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	entries := makeEntries(15) // more than maxBatchSize
	count, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 15 {
		t.Errorf("expected count=15, got %d", count)
	}

	// Give the flush a moment to complete (it's triggered inline by Enqueue)
	time.Sleep(50 * time.Millisecond)

	if ms.totalInserted() == 0 {
		t.Error("expected flush to have inserted some entries")
	}
}

func TestEnqueue_OverflowFallsBackToSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  5, // tiny queue
		MaxBatchSize:  5,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	// Enqueue more than the queue can hold
	entries := makeEntries(10)
	count, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 10 {
		t.Errorf("expected count=10 (enqueued + sync), got %d", count)
	}
	if q.OverflowCount() < 1 {
		t.Errorf("expected overflow count >= 1, got %d", q.OverflowCount())
	}

	// Wait briefly for async flush
	time.Sleep(50 * time.Millisecond)

	// All entries should eventually be in the store (some via queue, some via sync)
	if ms.totalInserted() < 10 {
		t.Errorf("expected all 10 entries to be inserted, got %d", ms.totalInserted())
	}
}

func TestEnqueue_AfterStop_FallsBackToSync(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	q.Stop()

	entries := makeEntries(3)
	count, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("expected count=3, got %d", count)
	}
	// Should have gone directly to the store
	if ms.totalInserted() != 3 {
		t.Errorf("expected 3 sync-inserted, got %d", ms.totalInserted())
	}
}

func TestEnqueue_StoreError_Propagated(t *testing.T) {
	storeErr := errors.New("disk full")
	ms := &queueMockLogStore{batchErr: storeErr}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  2, // tiny queue to force overflow
		MaxBatchSize:  2,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	entries := makeEntries(5) // will overflow → sync insert → error
	_, err := q.Enqueue(context.Background(), entries)
	if err == nil {
		t.Fatal("expected error from store, got nil")
	}
	if !errors.Is(err, storeErr) {
		t.Errorf("expected %v, got %v", storeErr, err)
	}
}

// ---------------------------------------------------------------------------
// Tests: Flush
// ---------------------------------------------------------------------------

func TestFlush_DrainsBuffer(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	entries := makeEntries(7)
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify entries are buffered
	if q.QueueDepth() != 7 {
		t.Fatalf("expected depth=7 before flush, got %d", q.QueueDepth())
	}

	q.Flush()

	if q.QueueDepth() != 0 {
		t.Errorf("expected depth=0 after flush, got %d", q.QueueDepth())
	}
	if ms.totalInserted() != 7 {
		t.Errorf("expected 7 inserted after flush, got %d", ms.totalInserted())
	}
}

func TestFlush_EmptyBuffer_NoOp(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	q.Flush() // should be a no-op

	if ms.callCount() != 0 {
		t.Errorf("expected no BatchInsert calls on empty flush, got %d", ms.callCount())
	}
}

func TestFlush_SplitsIntoBatches(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  5, // small batch size
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	entries := makeEntries(13) // should produce 3 batches: 5+5+3
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The enqueue should have triggered a flush since depth (13) >= maxBatchSize (5)
	// Give a brief moment for the flush to run
	time.Sleep(50 * time.Millisecond)

	if ms.totalInserted() != 13 {
		t.Errorf("expected 13 total inserted, got %d", ms.totalInserted())
	}
	if ms.callCount() < 2 {
		t.Errorf("expected at least 2 BatchInsert calls (split batches), got %d", ms.callCount())
	}
}

// ---------------------------------------------------------------------------
// Tests: Stop
// ---------------------------------------------------------------------------

func TestStop_FinalFlush(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second, // long interval, won't fire before Stop
	})

	entries := makeEntries(3)
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Entries should be buffered, not yet flushed
	if ms.totalInserted() != 0 {
		t.Errorf("expected 0 inserted before Stop, got %d", ms.totalInserted())
	}

	q.Stop()

	// After Stop, the final flush should have pushed entries to the store
	if ms.totalInserted() != 3 {
		t.Errorf("expected 3 inserted after Stop, got %d", ms.totalInserted())
	}
}

func TestStop_Idempotent(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})

	q.Stop()
	q.Stop() // second call should not panic or block
}

func TestStop_GracefulShutdown(t *testing.T) {
	ms := &queueMockLogStore{insertDelay: 10 * time.Millisecond}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  1000,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})

	entries := makeEntries(50)
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		q.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK - stopped gracefully
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not complete within 5 seconds (deadlock?)")
	}

	if ms.totalInserted() != 50 {
		t.Errorf("expected 50 entries flushed on Stop, got %d", ms.totalInserted())
	}
}

// ---------------------------------------------------------------------------
// Tests: Metrics (QueueDepth, FlushCount, OverflowCount)
// ---------------------------------------------------------------------------

func TestQueueDepth(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	if q.QueueDepth() != 0 {
		t.Errorf("initial depth should be 0, got %d", q.QueueDepth())
	}

	_, _ = q.Enqueue(context.Background(), makeEntries(3))
	if q.QueueDepth() != 3 {
		t.Errorf("after enqueue 3, depth should be 3, got %d", q.QueueDepth())
	}

	q.Flush()
	if q.QueueDepth() != 0 {
		t.Errorf("after flush, depth should be 0, got %d", q.QueueDepth())
	}
}

func TestFlushCount(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	if q.FlushCount() != 0 {
		t.Errorf("initial flush count should be 0, got %d", q.FlushCount())
	}

	_, _ = q.Enqueue(context.Background(), makeEntries(5))
	q.Flush()

	if q.FlushCount() < 1 {
		t.Errorf("expected flush count >= 1 after Flush, got %d", q.FlushCount())
	}
}

func TestOverflowCount(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  3,
		MaxBatchSize:  3,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	if q.OverflowCount() != 0 {
		t.Errorf("initial overflow count should be 0, got %d", q.OverflowCount())
	}

	// Enqueue more than the queue can hold
	_, _ = q.Enqueue(context.Background(), makeEntries(10))

	if q.OverflowCount() < 1 {
		t.Errorf("expected overflow count >= 1, got %d", q.OverflowCount())
	}
}

func TestTimerFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,                   // large batch size (won't trigger from enqueue)
		FlushInterval: 50 * time.Millisecond, // short interval to test timer
	})
	defer q.Stop()

	_, _ = q.Enqueue(context.Background(), makeEntries(3))

	// Wait for the timer-based flush to fire
	time.Sleep(200 * time.Millisecond)

	if ms.totalInserted() != 3 {
		t.Errorf("expected 3 entries flushed by timer, got %d", ms.totalInserted())
	}
	if q.QueueDepth() != 0 {
		t.Errorf("expected depth=0 after timer flush, got %d", q.QueueDepth())
	}
}

func TestEnqueue_ConcurrentSafety(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  10000,
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond,
	})
	defer q.Stop()

	const goroutines = 10
	const entriesPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := q.Enqueue(context.Background(), makeEntries(entriesPerGoroutine))
			if err != nil {
				t.Errorf("concurrent enqueue error: %v", err)
			}
		}()
	}
	wg.Wait()

	q.Stop()

	expected := int64(goroutines * entriesPerGoroutine)
	if ms.totalInserted() != expected {
		t.Errorf("expected %d total inserted after concurrent enqueue, got %d", expected, ms.totalInserted())
	}
}
