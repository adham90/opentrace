package jobs

import (
	"context"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchedulerEnqueuesJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	s := NewScheduler(q)
	s.Add(Schedule{
		Name:     "test-schedule",
		JobType:  "scheduled.task",
		Payload:  map[string]string{"source": "scheduler"},
		Interval: 100 * time.Millisecond,
	})

	s.Start(ctx)
	defer s.Stop()

	// Wait for the scheduler to enqueue at least one job.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for scheduled job to be enqueued")
		default:
			stats, err := q.Stats(ctx)
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			if stats[StatusPending] > 0 {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestSchedulerSkipsDuplicates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	// Pre-enqueue a pending job of the same type.
	if _, err := q.Enqueue(ctx, "scheduled.task", nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	s := NewScheduler(q)
	s.Add(Schedule{
		Name:     "test-schedule",
		JobType:  "scheduled.task",
		Payload:  nil,
		Interval: 100 * time.Millisecond,
	})

	s.Start(ctx)

	// Let a few ticks pass, then verify no duplicates were created.
	done := make(chan struct{})
	go func() {
		time.Sleep(350 * time.Millisecond)
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scheduler to stop")
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StatusPending] != 1 {
		t.Errorf("pending = %d, want 1 (should not create duplicates)", stats[StatusPending])
	}
}

// TestSchedulerFiresInitialRun proves a schedule enqueues immediately at
// startup rather than waiting a full interval. Without the initial run a job on
// a 6h schedule never fires on a process that restarts more often than that.
func TestSchedulerFiresInitialRun(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	s := NewScheduler(q)
	s.Add(Schedule{
		Name:     "long-interval",
		JobType:  "retention.jobs",
		Interval: time.Hour, // far longer than the test
	})

	s.Start(ctx)
	defer s.Stop()

	deadline := time.After(2 * time.Second)
	for {
		stats, err := q.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if stats[StatusPending] > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("schedule with a 1h interval never enqueued an initial run")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
