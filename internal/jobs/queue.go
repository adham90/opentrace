package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Status represents the state of a job in the queue.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusDead      Status = "dead"
)

// maxBackoff caps the exponential retry delay.
const maxBackoff = 5 * time.Minute

// Job represents a single unit of work in the queue.
type Job struct {
	ID          int64           `json:"id"`
	Queue       string          `json:"queue"`
	JobType     string          `json:"job_type"`
	Payload     json.RawMessage `json:"payload"`
	Status      Status          `json:"status"`
	Priority    int             `json:"priority"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	RunAt       time.Time       `json:"run_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Queue provides SQLite-backed job queue operations.
type Queue struct {
	db *sql.DB
}

// NewQueue creates a new Queue backed by the given database.
func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db}
}

// Enqueue adds a job to the default queue with run_at set to now.
func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any) (*Job, error) {
	return q.EnqueueAt(ctx, jobType, payload, time.Now())
}

// EnqueueAt schedules a job for a specific time.
func (q *Queue) EnqueueAt(ctx context.Context, jobType string, payload any, runAt time.Time) (*Job, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	runAtStr := runAt.UTC().Format(time.RFC3339)

	res, err := q.db.ExecContext(ctx,
		`INSERT INTO jobs (queue, job_type, payload, status, run_at, created_at)
		 VALUES ('default', ?, ?, 'pending', ?, ?)`,
		jobType, string(data), runAtStr, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert job: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}

	return q.getByID(ctx, id)
}

// ClaimNext atomically claims the next available job from the given queue.
// Returns nil, nil if no jobs are available.
func (q *Queue) ClaimNext(ctx context.Context, queue string) (*Job, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	var id int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM jobs
		 WHERE queue = ? AND status = 'pending' AND run_at <= ?
		 ORDER BY priority DESC, created_at ASC
		 LIMIT 1`,
		queue, now,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select job: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', started_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return q.getByID(ctx, id)
}

// Complete marks a job as completed.
func (q *Queue) Complete(ctx context.Context, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := q.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'completed', completed_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("complete job %d: %w", id, err)
	}
	return nil
}

// Fail marks a job as failed. If attempts < max_attempts, it schedules a retry
// with exponential backoff (2^attempts * 5s, capped at 5 minutes).
// If max_attempts is reached, the job is marked as "dead".
func (q *Queue) Fail(ctx context.Context, id int64, jobErr error) error {
	job, err := q.getByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get job %d: %w", id, err)
	}

	newAttempts := job.Attempts + 1
	errMsg := ""
	if jobErr != nil {
		errMsg = jobErr.Error()
	}

	if newAttempts >= job.MaxAttempts {
		_, err = q.db.ExecContext(ctx,
			`UPDATE jobs SET status = 'dead', attempts = ?, last_error = ? WHERE id = ?`,
			newAttempts, errMsg, id,
		)
	} else {
		backoff := time.Duration(1<<uint(newAttempts)) * 5 * time.Second
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		runAt := time.Now().UTC().Add(backoff).Format(time.RFC3339)
		_, err = q.db.ExecContext(ctx,
			`UPDATE jobs SET status = 'pending', attempts = ?, last_error = ?, run_at = ? WHERE id = ?`,
			newAttempts, errMsg, runAt, id,
		)
	}
	if err != nil {
		return fmt.Errorf("fail job %d: %w", id, err)
	}
	return nil
}

// Retry re-queues a dead or failed job by resetting its status to pending with run_at = now.
func (q *Queue) Retry(ctx context.Context, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := q.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'pending', run_at = ? WHERE id = ? AND status IN ('dead', 'failed')`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("retry job %d: %w", id, err)
	}
	return nil
}

// Stats returns job counts grouped by status.
func (q *Queue) Stats(ctx context.Context) (map[Status]int, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM jobs GROUP BY status`,
	)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[Status]int)
	for rows.Next() {
		var s string
		var count int
		if err := rows.Scan(&s, &count); err != nil {
			return nil, fmt.Errorf("scan stats: %w", err)
		}
		stats[Status(s)] = count
	}
	return stats, rows.Err()
}

// Prune removes completed and dead jobs older than the given duration.
// Returns the number of rows deleted.
func (q *Queue) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := q.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE status IN ('completed', 'dead') AND created_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("prune jobs: %w", err)
	}
	return res.RowsAffected()
}

// getByID fetches a single job by its primary key.
func (q *Queue) getByID(ctx context.Context, id int64) (*Job, error) {
	var j Job
	var payload string
	var runAt, createdAt string
	var startedAt, completedAt, lastError sql.NullString
	var status string

	err := q.db.QueryRowContext(ctx,
		`SELECT id, queue, job_type, payload, status, priority, attempts, max_attempts,
		        run_at, started_at, completed_at, last_error, created_at
		 FROM jobs WHERE id = ?`, id,
	).Scan(
		&j.ID, &j.Queue, &j.JobType, &payload, &status,
		&j.Priority, &j.Attempts, &j.MaxAttempts,
		&runAt, &startedAt, &completedAt, &lastError, &createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get job %d: %w", id, err)
	}

	j.Status = Status(status)
	j.Payload = json.RawMessage(payload)

	if t, err := time.Parse(time.RFC3339, runAt); err == nil {
		j.RunAt = t
	}
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		j.CreatedAt = t
	}
	if startedAt.Valid {
		if t, err := time.Parse(time.RFC3339, startedAt.String); err == nil {
			j.StartedAt = &t
		}
	}
	if completedAt.Valid {
		if t, err := time.Parse(time.RFC3339, completedAt.String); err == nil {
			j.CompletedAt = &t
		}
	}
	if lastError.Valid {
		j.LastError = lastError.String
	}

	return &j, nil
}
