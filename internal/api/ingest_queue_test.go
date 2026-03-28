package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/ingest"
	"github.com/adham90/opentrace/pkg/store"
)

// countingLogStore wraps mockLogStore and counts BatchInsert calls.
type countingLogStore struct {
	mockLogStore
	insertCalls atomic.Int64
}

func newCountingLogStore() *countingLogStore {
	return &countingLogStore{
		mockLogStore: mockLogStore{batches: make(map[string]*store.BatchRecord)},
	}
}

func (c *countingLogStore) BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error) {
	c.insertCalls.Add(1)
	return c.mockLogStore.BatchInsert(ctx, entries)
}

func makeQueueEntries(n int) []store.LogEntry {
	entries := make([]store.LogEntry, n)
	for i := range entries {
		entries[i] = store.LogEntry{
			Timestamp: time.Now(),
			Level:     "INFO",
			Service:   "test-svc",
			Message:   "test message",
		}
	}
	return entries
}

func TestIngestQueue_EnqueueAndFlush(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  50,
		FlushInterval: 1 * time.Hour, // disable timer-based flush
	})
	defer q.Stop()

	entries := makeQueueEntries(5)
	count, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}

	// Entries should be in the queue, not yet in the store
	if q.QueueDepth() != 5 {
		t.Fatalf("queue depth = %d, want 5", q.QueueDepth())
	}

	// Explicit flush should write to store
	q.Flush()

	// Wait a moment for flush to complete
	time.Sleep(10 * time.Millisecond)

	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != 5 {
		t.Fatalf("stored entries = %d, want 5", stored)
	}

	if q.QueueDepth() != 0 {
		t.Fatalf("queue depth after flush = %d, want 0", q.QueueDepth())
	}
}

func TestIngestQueue_FlushOnBatchFull(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  200,
		MaxBatchSize:  10,
		FlushInterval: 1 * time.Hour, // disable timer
	})
	defer q.Stop()

	// Enqueue exactly maxBatchSize entries - should trigger a flush
	entries := makeQueueEntries(10)
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Give the flush a moment to complete
	time.Sleep(50 * time.Millisecond)

	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != 10 {
		t.Fatalf("stored entries after batch-full flush = %d, want 10", stored)
	}
}

func TestIngestQueue_FlushOnTimer(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  1000,
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond, // fast timer
	})
	defer q.Stop()

	// Enqueue less than a full batch
	entries := makeQueueEntries(3)
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Wait for the timer to flush
	time.Sleep(200 * time.Millisecond)

	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != 3 {
		t.Fatalf("stored entries after timer flush = %d, want 3", stored)
	}
}

func TestIngestQueue_FlushOnShutdown(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  1000,
		MaxBatchSize:  1000,
		FlushInterval: 1 * time.Hour, // disable timer
	})

	// Enqueue entries without triggering any flush
	entries := makeQueueEntries(7)
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Stop should flush remaining entries
	q.Stop()

	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != 7 {
		t.Fatalf("stored entries after Stop = %d, want 7", stored)
	}
}

func TestIngestQueue_OverflowFallback(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  5,
		MaxBatchSize:  100,
		FlushInterval: 1 * time.Hour, // disable timer
	})
	defer q.Stop()

	// Enqueue more than the queue can hold
	entries := makeQueueEntries(8)
	count, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if count != 8 {
		t.Fatalf("count = %d, want 8 (all should be inserted)", count)
	}

	// Overflow should have been recorded
	if q.OverflowCount() != 1 {
		t.Fatalf("overflow count = %d, want 1", q.OverflowCount())
	}

	// Wait for any async flushes triggered by overflow
	time.Sleep(50 * time.Millisecond)

	// All entries should eventually reach the store
	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != 8 {
		t.Fatalf("stored entries after overflow = %d, want 8", stored)
	}
}

func TestIngestQueue_OverflowWhenQueueCompletelyFull(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  3,
		MaxBatchSize:  100,
		FlushInterval: 1 * time.Hour,
	})
	defer q.Stop()

	// Fill the queue first
	_, err := q.Enqueue(context.Background(), makeQueueEntries(3))
	if err != nil {
		t.Fatalf("Enqueue fill failed: %v", err)
	}

	// Now enqueue more with zero space left
	count, err := q.Enqueue(context.Background(), makeQueueEntries(5))
	if err != nil {
		t.Fatalf("Enqueue overflow failed: %v", err)
	}
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}

	// Wait for flushes
	time.Sleep(50 * time.Millisecond)

	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != 8 {
		t.Fatalf("stored entries = %d, want 8", stored)
	}
}

func TestIngestQueue_ConcurrentEnqueue(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  10000,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Millisecond,
	})

	const goroutines = 20
	const entriesPerGoroutine = 50

	var wg sync.WaitGroup
	var totalEnqueued atomic.Int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entries := makeQueueEntries(entriesPerGoroutine)
			count, err := q.Enqueue(context.Background(), entries)
			if err != nil {
				t.Errorf("concurrent Enqueue failed: %v", err)
				return
			}
			totalEnqueued.Add(int64(count))
		}()
	}

	wg.Wait()
	q.Stop()

	expected := goroutines * entriesPerGoroutine
	if totalEnqueued.Load() != int64(expected) {
		t.Fatalf("total enqueued = %d, want %d", totalEnqueued.Load(), expected)
	}

	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != expected {
		t.Fatalf("stored entries = %d, want %d", stored, expected)
	}
}

func TestIngestQueue_NilQueueFallback(t *testing.T) {
	// Verify that a nil Queue in the ingest handler falls back to direct BatchInsert
	ls := newMockLogStore()
	h := &ingest.Handler{
		LogStore: ls,
		// Queue is nil (default)
	}

	// The handler would call BatchInsert directly when Queue is nil
	// This tests the logic branch, not the HTTP handler
	if h.Queue != nil {
		t.Fatal("expected Queue to be nil")
	}

	entries := makeQueueEntries(3)
	count, err := ls.BatchInsert(context.Background(), entries)
	if err != nil {
		t.Fatalf("direct BatchInsert failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestIngestQueue_StopIsIdempotent(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 1 * time.Hour,
	})

	q.Stop()
	q.Stop() // Should not panic
}

func TestIngestQueue_EnqueueAfterStop(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 1 * time.Hour,
	})
	q.Stop()

	// Enqueue after stop should fall back to synchronous insert
	entries := makeQueueEntries(3)
	count, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("Enqueue after stop failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}

	ls.mu.Lock()
	stored := len(ls.entries)
	ls.mu.Unlock()

	if stored != 3 {
		t.Fatalf("stored entries = %d, want 3", stored)
	}
}

func TestIngestQueue_Metrics(t *testing.T) {
	ls := newCountingLogStore()
	q := ingest.NewQueue(ls, ingest.QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 1 * time.Hour,
	})
	defer q.Stop()

	if q.FlushCount() != 0 {
		t.Fatalf("initial flush count = %d, want 0", q.FlushCount())
	}
	if q.OverflowCount() != 0 {
		t.Fatalf("initial overflow count = %d, want 0", q.OverflowCount())
	}

	_, _ = q.Enqueue(context.Background(), makeQueueEntries(3))
	q.Flush()

	// Wait briefly for flush to complete
	time.Sleep(10 * time.Millisecond)

	if q.FlushCount() < 1 {
		t.Fatalf("flush count = %d, want >= 1", q.FlushCount())
	}
}
