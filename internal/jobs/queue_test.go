package jobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const createJobsTable = `
CREATE TABLE IF NOT EXISTS jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    queue TEXT NOT NULL DEFAULT 'default',
    job_type TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 0,
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    run_at TEXT NOT NULL DEFAULT (datetime('now')),
    started_at TEXT,
    completed_at TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_jobs_claim ON jobs(queue, status, run_at, priority);
CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(job_type);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
`

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use a temp file so all connections share the same database.
	dbPath := t.TempDir() + "/test.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening test SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(createJobsTable); err != nil {
		db.Close()
		t.Fatalf("creating jobs table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnqueue(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	job, err := q.Enqueue(ctx, "email.send", map[string]string{"to": "user@example.com"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job.ID == 0 {
		t.Fatal("expected non-zero job ID")
	}
	if job.JobType != "email.send" {
		t.Errorf("job_type = %q, want %q", job.JobType, "email.send")
	}
	if job.Status != StatusPending {
		t.Errorf("status = %q, want %q", job.Status, StatusPending)
	}
	if job.Queue != "default" {
		t.Errorf("queue = %q, want %q", job.Queue, "default")
	}
	if job.MaxAttempts != 3 {
		t.Errorf("max_attempts = %d, want 3", job.MaxAttempts)
	}
}

func TestClaimNext(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	_, err := q.Enqueue(ctx, "test.job", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	job, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job, got nil")
	}
	if job.Status != StatusRunning {
		t.Errorf("status = %q, want %q", job.Status, StatusRunning)
	}
	if job.StartedAt == nil {
		t.Error("expected started_at to be set")
	}
}

func TestClaimNextEmpty(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	job, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if job != nil {
		t.Fatalf("expected nil job, got %+v", job)
	}
}

func TestComplete(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	enqueued, err := q.Enqueue(ctx, "test.job", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	if err := q.Complete(ctx, claimed.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	job, err := q.getByID(ctx, enqueued.ID)
	if err != nil {
		t.Fatalf("getByID: %v", err)
	}
	if job.Status != StatusCompleted {
		t.Errorf("status = %q, want %q", job.Status, StatusCompleted)
	}
	if job.CompletedAt == nil {
		t.Error("expected completed_at to be set")
	}
}

func TestFail(t *testing.T) {
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

	if err := q.Fail(ctx, claimed.ID, errors.New("connection timeout")); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	job, err := q.getByID(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("getByID: %v", err)
	}
	if job.Status != StatusPending {
		t.Errorf("status = %q, want %q (retry)", job.Status, StatusPending)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", job.Attempts)
	}
	if job.LastError != "connection timeout" {
		t.Errorf("last_error = %q, want %q", job.LastError, "connection timeout")
	}
}

func TestFailMaxAttempts(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	// Insert a job with max_attempts=1 directly so first failure kills it.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, max_attempts, run_at, created_at)
		 VALUES ('default', 'test.job', '{}', 'pending', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	if err := q.Fail(ctx, claimed.ID, errors.New("fatal error")); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	job, err := q.getByID(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("getByID: %v", err)
	}
	if job.Status != StatusDead {
		t.Errorf("status = %q, want %q", job.Status, StatusDead)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", job.Attempts)
	}
}

func TestStats(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	// Enqueue 3 jobs.
	for i := 0; i < 3; i++ {
		if _, err := q.Enqueue(ctx, "test.job", nil); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	// Claim and complete one.
	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if err := q.Complete(ctx, claimed.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	if stats[StatusPending] != 2 {
		t.Errorf("pending = %d, want 2", stats[StatusPending])
	}
	if stats[StatusCompleted] != 1 {
		t.Errorf("completed = %d, want 1", stats[StatusCompleted])
	}
}

func TestPrune(t *testing.T) {
	db := setupTestDB(t)
	q := NewQueue(db)
	ctx := context.Background()

	// Insert an old completed job directly.
	oldTime := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, run_at, created_at, completed_at)
		 VALUES ('default', 'old.job', '{}', 'completed', ?, ?, ?)`,
		oldTime, oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert old job: %v", err)
	}

	// Insert a recent completed job.
	if _, err := q.Enqueue(ctx, "new.job", nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed, err := q.ClaimNext(ctx, "default")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if err := q.Complete(ctx, claimed.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Prune jobs older than 24 hours.
	pruned, err := q.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	// Verify the recent job is still there.
	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StatusCompleted] != 1 {
		t.Errorf("completed after prune = %d, want 1", stats[StatusCompleted])
	}
}
