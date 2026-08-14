package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// WithQueueName option (0% coverage)
// ---------------------------------------------------------------------------

func TestWithQueueName(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)

	w := NewWorker(q, WithQueueName("high-priority"))
	if w.queueName != "high-priority" {
		t.Errorf("queueName = %q, want %q", w.queueName, "high-priority")
	}
}

func TestWithQueueName_DefaultIsDefault(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)

	w := NewWorker(q)
	if w.queueName != "default" {
		t.Errorf("default queueName = %q, want %q", w.queueName, "default")
	}
}

// ---------------------------------------------------------------------------
// Retry (0% coverage)
// ---------------------------------------------------------------------------

func TestRetry_DeadJob(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	// Insert a dead job directly with max_attempts=1
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, max_attempts, attempts, last_error, run_at, created_at)
		 VALUES ('default', 'test.job', '{}', 'dead', 1, 1, 'fatal error', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Retry the dead job
	if err := q.Retry(ctx, 1); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	job, err := q.getByID(ctx, 1)
	if err != nil {
		t.Fatalf("getByID: %v", err)
	}
	if job.Status != StatusPending {
		t.Errorf("status = %q, want %q after retry", job.Status, StatusPending)
	}
}

func TestRetry_FailedJob(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	// Create and fail a job
	enqueued, err := q.Enqueue(ctx, "test.job", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	// Fail it enough times to go dead (max_attempts is 3)
	for i := 0; i < 3; i++ {
		if err := q.Fail(ctx, claimed.ID, errors.New("fail")); err != nil {
			t.Fatalf("Fail(%d): %v", i, err)
		}
		// Re-claim if not dead yet
		job, _ := q.getByID(ctx, claimed.ID)
		if job.Status == StatusDead {
			break
		}
		// Update run_at to now so we can claim it again
		db.ExecContext(ctx, `UPDATE jobs SET run_at = ? WHERE id = ?`,
			time.Now().UTC().Format(time.RFC3339), claimed.ID)
		claimed, err = q.ClaimNext(ctx, "default")
		if err != nil {
			t.Fatalf("ClaimNext retry: %v", err)
		}
	}

	job, _ := q.getByID(ctx, enqueued.ID)
	if job.Status != StatusDead {
		t.Fatalf("expected dead status, got %q", job.Status)
	}

	// Retry the dead job
	if err := q.Retry(ctx, enqueued.ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	job, _ = q.getByID(ctx, enqueued.ID)
	if job.Status != StatusPending {
		t.Errorf("status = %q, want %q after retry", job.Status, StatusPending)
	}
}

func TestRetry_PendingJob_NoOp(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	enqueued, err := q.Enqueue(ctx, "test.job", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Retry a pending job — the WHERE clause requires status IN ('dead','failed'),
	// so this should not change the job.
	if err := q.Retry(ctx, enqueued.ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	job, _ := q.getByID(ctx, enqueued.ID)
	if job.Status != StatusPending {
		t.Errorf("status = %q, expected still pending", job.Status)
	}
}

// ---------------------------------------------------------------------------
// Worker: handler returns error path (processNext 70%)
// ---------------------------------------------------------------------------

func TestWorkerHandlerError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	var called atomic.Bool

	w := NewWorker(q, WithPollInterval(50*time.Millisecond))
	w.Register("failing.job", func(_ context.Context, payload json.RawMessage) error {
		called.Store(true)
		return errors.New("handler exploded")
	})

	if _, err := q.Enqueue(ctx, "failing.job", nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	w.Start(ctx)
	defer w.Stop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for handler error to be recorded")
		default:
			job, err := q.getByID(ctx, 1)
			if err != nil {
				t.Fatalf("getByID: %v", err)
			}
			if job.Attempts > 0 {
				if job.LastError != "handler exploded" {
					t.Errorf("last_error = %q, want %q", job.LastError, "handler exploded")
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// ---------------------------------------------------------------------------
// Worker: Start and Stop lifecycle
// ---------------------------------------------------------------------------

func TestWorkerStartStop_NoJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	w := NewWorker(q, WithPollInterval(50*time.Millisecond))
	w.Register("any.job", func(_ context.Context, payload json.RawMessage) error {
		return nil
	})

	w.Start(ctx)

	// Let a few polls pass, then stop. Use a deadline to guard against hangs.
	done := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK — should not hang or panic
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worker to stop")
	}
}

func TestWorkerStop_NilCancel(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)

	w := NewWorker(q)
	// Stop without Start — cancel is nil
	w.Stop() // should not panic
}

// ---------------------------------------------------------------------------
// Worker: multiple options combined
// ---------------------------------------------------------------------------

func TestWorkerMultipleOptions(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)

	w := NewWorker(q,
		WithQueueName("batch"),
		WithPollInterval(200*time.Millisecond),
	)
	if w.queueName != "batch" {
		t.Errorf("queueName = %q, want %q", w.queueName, "batch")
	}
	if w.pollInterval != 200*time.Millisecond {
		t.Errorf("pollInterval = %v, want 200ms", w.pollInterval)
	}
}

// ---------------------------------------------------------------------------
// Queue: Fail with nil error
// ---------------------------------------------------------------------------

func TestFail_NilError(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	_, err := q.Enqueue(ctx, "test.job", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	// Fail with nil error
	if err := q.Fail(ctx, claimed.ID, nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	job, err := q.getByID(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("getByID: %v", err)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", job.Attempts)
	}
	if job.LastError != "" {
		t.Errorf("last_error = %q, want empty for nil error", job.LastError)
	}
}

// ---------------------------------------------------------------------------
// Queue: EnqueueAt with specific future time
// ---------------------------------------------------------------------------

func TestEnqueueAt_FutureTime(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	futureTime := time.Now().Add(1 * time.Hour)
	job, err := q.EnqueueAt(ctx, "delayed.job", map[string]string{"key": "value"}, futureTime)
	if err != nil {
		t.Fatalf("EnqueueAt: %v", err)
	}
	if job.Status != StatusPending {
		t.Errorf("status = %q, want %q", job.Status, StatusPending)
	}

	// Should not be claimable since run_at is in the future
	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed != nil {
		t.Fatal("should not claim a future job")
	}
}

// TestWorkerCompletesJobAfterContextCancel proves the completion write uses a
// context detached from the poll context. Shutdown cancels the poll context
// before the in-flight handler returns; if Complete used that context the row
// would stay 'running' and the finished work would be re-executed after the
// next boot's reap.
func TestWorkerCompletesJobAfterContextCancel(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job, err := q.Enqueue(ctx, "shutdown.task", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	done := make(chan struct{})
	w := NewWorker(q,
		WithPollInterval(10*time.Millisecond),
		WithReapInterval(0),
	)
	w.Register("shutdown.task", func(context.Context, json.RawMessage) error {
		// Simulate SIGTERM arriving while the handler is finishing.
		cancel()
		close(done)
		return nil
	})

	w.Start(ctx)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		w.Stop()
		t.Fatal("handler never ran")
	}
	w.Stop()

	got, err := q.getByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Errorf("job status = %q, want %q (last_error=%q)", got.Status, StatusCompleted, got.LastError)
	}
}

// TestWorkerReapsWedgedRunningJob proves the worker reclaims a job stuck in
// 'running' past the visibility timeout, so a failed bookkeeping write cannot
// block a job type for the rest of the process's lifetime.
func TestWorkerReapsWedgedRunningJob(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	job, err := q.Enqueue(ctx, "wedged.task", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', started_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), job.ID,
	); err != nil {
		t.Fatalf("wedge job: %v", err)
	}

	w := NewWorker(q,
		WithPollInterval(time.Hour), // never claim; only the reaper should act
		WithReapInterval(10*time.Millisecond),
		WithVisibilityTimeout(time.Minute),
	)
	w.Start(ctx)
	defer w.Stop()

	deadline := time.After(3 * time.Second)
	for {
		got, err := q.getByID(ctx, job.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status != StatusRunning {
			return // reclaimed (pending or dead)
		}
		select {
		case <-deadline:
			t.Fatal("wedged 'running' job was never reclaimed by the periodic reap")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
