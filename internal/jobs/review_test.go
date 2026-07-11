package jobs

import (
	"context"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestReapOrphaned_ReclaimsRunning proves a job stuck in 'running' is reclaimed
// back to 'pending' and becomes claimable again.
func TestReapOrphaned_ReclaimsRunning(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, started_at, run_at, created_at)
		 VALUES ('default', 'stuck.job', '{}', 'running', ?, ?, ?)`,
		old, old, old,
	)
	if err != nil {
		t.Fatalf("insert running job: %v", err)
	}

	n, err := q.ReapOrphaned(ctx, 0)
	if err != nil {
		t.Fatalf("ReapOrphaned: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1", n)
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StatusRunning] != 0 {
		t.Errorf("running after reap = %d, want 0", stats[StatusRunning])
	}
	if stats[StatusPending] != 1 {
		t.Errorf("pending after reap = %d, want 1", stats[StatusPending])
	}

	// The reclaimed job must be claimable again.
	job, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if job == nil {
		t.Fatal("expected reclaimed job to be claimable, got nil")
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (incremented on reclaim)", job.Attempts)
	}
	if !strings.Contains(job.LastError, "reclaimed") {
		t.Errorf("last_error = %q, want it to mention reclaim", job.LastError)
	}
}

// TestReapOrphaned_ExhaustedAttemptsDies proves an orphaned job with no retry
// budget left is marked dead rather than re-run forever.
func TestReapOrphaned_ExhaustedAttemptsDies(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	// attempts already at max_attempts-1, so attempts+1 >= max_attempts.
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, attempts, max_attempts, started_at, run_at, created_at)
		 VALUES ('default', 'stuck.job', '{}', 'running', 2, 3, ?, ?, ?)`,
		now, now, now,
	)
	if err != nil {
		t.Fatalf("insert running job: %v", err)
	}

	n, err := q.ReapOrphaned(ctx, 0)
	if err != nil {
		t.Fatalf("ReapOrphaned: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1", n)
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StatusDead] != 1 {
		t.Errorf("dead = %d, want 1", stats[StatusDead])
	}
	if stats[StatusPending] != 0 {
		t.Errorf("pending = %d, want 0", stats[StatusPending])
	}
}

// TestReapOrphaned_VisibilityTimeout proves that with a deadline only stale
// running rows are reclaimed — a freshly-started one is left alone.
func TestReapOrphaned_VisibilityTimeout(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	stale := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	for _, sa := range []string{stale, fresh} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO jobs (queue, job_type, payload, status, started_at, run_at, created_at)
			 VALUES ('default', 'job', '{}', 'running', ?, ?, ?)`,
			sa, sa, sa,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// 10-minute visibility timeout: only the 30-min-old row is orphaned.
	n, err := q.ReapOrphaned(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("ReapOrphaned: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1 (only the stale one)", n)
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StatusRunning] != 1 {
		t.Errorf("running = %d, want 1 (fresh row untouched)", stats[StatusRunning])
	}
	if stats[StatusPending] != 1 {
		t.Errorf("pending = %d, want 1 (stale row reclaimed)", stats[StatusPending])
	}
}

// TestReapOrphaned_UnblocksSchedule proves the end-to-end fix: a wedged
// 'running' row causes maybeEnqueue to skip the recurring job forever; after
// reaping and draining, the schedule resumes.
func TestReapOrphaned_UnblocksSchedule(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, started_at, run_at, created_at)
		 VALUES ('default', 'recurring.task', '{}', 'running', ?, ?, ?)`,
		old, old, old,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	s := NewScheduler(q)
	sched := Schedule{Name: "recurring", JobType: "recurring.task", Interval: time.Hour}

	// Wedged: a 'running' row makes maybeEnqueue skip the job type.
	s.maybeEnqueue(ctx, sched)
	stats, _ := q.Stats(ctx)
	if stats[StatusPending] != 0 {
		t.Fatalf("pending = %d, want 0 (schedule blocked by stuck running row)", stats[StatusPending])
	}

	// Reap + drain the reclaimed job.
	if _, err := q.ReapOrphaned(ctx, 0); err != nil {
		t.Fatalf("ReapOrphaned: %v", err)
	}
	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext after reap: job=%v err=%v", claimed, err)
	}
	if err := q.Complete(ctx, claimed.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Schedule resumes: no pending/running rows block it now.
	s.maybeEnqueue(ctx, sched)
	stats, _ = q.Stats(ctx)
	if stats[StatusPending] != 1 {
		t.Errorf("pending = %d, want 1 (schedule resumed after reap)", stats[StatusPending])
	}
}

// TestRunJobRetention_PrunesAndVacuums proves the retention job removes old jobs
// and that the VACUUM path runs (freed pages are reclaimed — freelist_count 0).
func TestRunJobRetention_PrunesAndVacuums(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Add the retention-cleanup tables so RunJobRetention exercises the real
	// table path too, not just the jobs prune.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE app_config (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '{}');
		CREATE TABLE metric_buckets (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL);
	`); err != nil {
		t.Fatalf("create aux tables: %v", err)
	}

	// Insert many old completed jobs with filler payloads spanning several pages
	// so deleting them frees pages that only VACUUM can reclaim.
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	filler := strings.Repeat("x", 256)
	for i := 0; i < 800; i++ {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO jobs (queue, job_type, payload, status, run_at, created_at, completed_at)
			 VALUES ('default', 'old.job', ?, 'completed', ?, ?, ?)`,
			filler, old, old, old,
		); err != nil {
			t.Fatalf("insert old job: %v", err)
		}
	}
	// One recent completed job that must survive.
	recent := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, run_at, created_at, completed_at)
		 VALUES ('default', 'new.job', '{}', 'completed', ?, ?, ?)`,
		recent, recent, recent,
	); err != nil {
		t.Fatalf("insert recent job: %v", err)
	}

	pruned, err := RunJobRetention(ctx, db, 24*time.Hour)
	if err != nil {
		t.Fatalf("RunJobRetention: %v", err)
	}
	if pruned != 800 {
		t.Errorf("pruned = %d, want 800", pruned)
	}

	// Recent job survived.
	q := NewQueue(db)
	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StatusCompleted] != 1 {
		t.Errorf("completed after prune = %d, want 1", stats[StatusCompleted])
	}

	// VACUUM ran: after deleting ~200KB of rows, the freelist is empty. Without
	// VACUUM the freed pages would sit on the freelist (freelist_count > 0).
	var freelist int
	if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freelist); err != nil {
		t.Fatalf("PRAGMA freelist_count: %v", err)
	}
	if freelist != 0 {
		t.Errorf("freelist_count = %d, want 0 (VACUUM should have reclaimed freed pages)", freelist)
	}
}
