package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// flushBatch with store error (error logging path)
// ---------------------------------------------------------------------------

func TestFlushBatch_StoreError(t *testing.T) {
	storeErr := errors.New("disk full")
	ms := &queueMockLogStore{batchErr: storeErr}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	entries := makeEntries(5)
	_, err := q.Enqueue(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Manually flush — should not panic even with store errors
	q.Flush()

	// flushBatch was called and incremented the counter despite the error
	if q.FlushCount() < 1 {
		t.Errorf("expected FlushCount >= 1 after flush with error, got %d", q.FlushCount())
	}
}

// ---------------------------------------------------------------------------
// flushBatch splits large batches into chunks
// ---------------------------------------------------------------------------

func TestFlushBatch_LargeBatchChunking(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  500,
		MaxBatchSize:  10,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	// Directly call flushBatch with a batch larger than maxBatchSize
	entries := makeEntries(25) // should produce 3 chunks: 10+10+5
	q.flushBatch(entries)

	if ms.callCount() != 3 {
		t.Errorf("expected 3 BatchInsert calls (chunks of 10+10+5), got %d", ms.callCount())
	}
	if ms.totalInserted() != 25 {
		t.Errorf("expected 25 total inserted, got %d", ms.totalInserted())
	}
}

// ---------------------------------------------------------------------------
// Enqueue: partial overflow (some entries fit, rest overflow)
// ---------------------------------------------------------------------------

func TestEnqueue_PartialOverflow(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  7,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	// First enqueue 5 entries (within capacity)
	count, err := q.Enqueue(context.Background(), makeEntries(5))
	if err != nil {
		t.Fatalf("first enqueue error: %v", err)
	}
	if count != 5 {
		t.Errorf("first count = %d, want 5", count)
	}

	// Now enqueue 5 more — only 2 fit (7-5=2), 3 overflow to sync insert
	count, err = q.Enqueue(context.Background(), makeEntries(5))
	if err != nil {
		t.Fatalf("second enqueue error: %v", err)
	}
	if count != 5 {
		t.Errorf("second count = %d, want 5 (2 enqueued + 3 sync)", count)
	}

	if q.OverflowCount() < 1 {
		t.Errorf("expected overflow count >= 1, got %d", q.OverflowCount())
	}

	// Wait for flush and verify all entries are inserted
	q.Stop()

	if ms.totalInserted() < 10 {
		t.Errorf("expected all 10 entries to be inserted, got %d", ms.totalInserted())
	}
}

// ---------------------------------------------------------------------------
// Enqueue: completely full queue (spaceAvailable == 0)
// ---------------------------------------------------------------------------

func TestEnqueue_CompletelyFullQueue(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  3,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	// Fill the queue exactly
	count, err := q.Enqueue(context.Background(), makeEntries(3))
	if err != nil {
		t.Fatalf("first enqueue error: %v", err)
	}
	if count != 3 {
		t.Errorf("first count = %d, want 3", count)
	}

	// Now enqueue more — queue is at capacity, all go to sync insert
	count, err = q.Enqueue(context.Background(), makeEntries(4))
	if err != nil {
		t.Fatalf("second enqueue error: %v", err)
	}
	if count != 4 {
		t.Errorf("second count = %d, want 4 (all sync)", count)
	}

	if q.OverflowCount() < 1 {
		t.Errorf("expected overflow count >= 1, got %d", q.OverflowCount())
	}
}

// ---------------------------------------------------------------------------
// Enqueue: after stop with store error
// ---------------------------------------------------------------------------

func TestEnqueue_AfterStop_StoreError(t *testing.T) {
	storeErr := errors.New("db unavailable")
	ms := &queueMockLogStore{batchErr: storeErr}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	q.Stop()

	// Enqueue after stop falls back to sync insert, which should propagate error
	_, err := q.Enqueue(context.Background(), makeEntries(2))
	if err == nil {
		t.Fatal("expected error from store, got nil")
	}
	if !errors.Is(err, storeErr) {
		t.Errorf("expected %v, got %v", storeErr, err)
	}
}

// ---------------------------------------------------------------------------
// Multiple flushes: verify FlushCount increments
// ---------------------------------------------------------------------------

func TestFlushCount_MultipleFlushes(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	for i := 0; i < 3; i++ {
		_, _ = q.Enqueue(context.Background(), makeEntries(1))
		q.Flush()
	}

	if q.FlushCount() < 3 {
		t.Errorf("expected FlushCount >= 3, got %d", q.FlushCount())
	}
}

// ---------------------------------------------------------------------------
// Ensure empty enqueue works
// ---------------------------------------------------------------------------

func TestEnqueue_EmptySlice(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	})
	defer q.Stop()

	count, err := q.Enqueue(context.Background(), []store.LogEntry{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
	if q.QueueDepth() != 0 {
		t.Errorf("depth = %d, want 0", q.QueueDepth())
	}
}

// ---------------------------------------------------------------------------
// Timer-based flush with store error (should not crash)
// ---------------------------------------------------------------------------

func TestTimerFlush_StoreError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	storeErr := errors.New("write error")
	ms := &queueMockLogStore{batchErr: storeErr}
	q := NewQueue(ms, QueueConfig{
		MaxQueueSize:  100,
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond,
	})

	_, _ = q.Enqueue(context.Background(), makeEntries(3))

	// Let the timer-based flush fire — it should handle the error gracefully
	time.Sleep(200 * time.Millisecond)

	q.Stop()

	// The flush should have attempted and incremented the counter
	if q.FlushCount() < 1 {
		t.Errorf("expected FlushCount >= 1 even with errors, got %d", q.FlushCount())
	}
}
