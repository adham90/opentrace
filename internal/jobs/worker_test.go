package jobs

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestWorkerProcessesJob(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	var called atomic.Bool

	w := NewWorker(q, WithPollInterval(50*time.Millisecond))
	w.Register("test.work", func(_ context.Context, payload json.RawMessage) error {
		called.Store(true)
		return nil
	})

	if _, err := q.Enqueue(ctx, "test.work", map[string]string{"key": "value"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	w.Start(ctx)
	defer w.Stop()

	// Wait for the worker to process the job.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for handler to be called")
		default:
			if called.Load() {
				// Verify the job is completed.
				stats, err := q.Stats(ctx)
				if err != nil {
					t.Fatalf("Stats: %v", err)
				}
				if stats[StatusCompleted] != 1 {
					t.Errorf("completed = %d, want 1", stats[StatusCompleted])
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestWorkerUnknownType(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	w := NewWorker(q, WithPollInterval(50*time.Millisecond))
	// No handlers registered.

	if _, err := q.Enqueue(ctx, "unknown.type", nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	w.Start(ctx)

	// Let the worker pick up and fail the job.
	time.Sleep(300 * time.Millisecond)
	w.Stop()

	// After the worker processes the unknown job type, it should have
	// been failed. With max_attempts=3 the first failure sets it back
	// to pending with a future run_at and incremented attempts.
	job, err := q.getByID(ctx, 1)
	if err != nil {
		t.Fatalf("getByID: %v", err)
	}
	if job.Attempts == 0 {
		t.Error("expected attempts > 0 after unknown job type failure")
	}
	if job.LastError == "" {
		t.Error("expected last_error to be set")
	}
}
