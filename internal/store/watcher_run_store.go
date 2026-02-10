package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// watcherRunStore implements WatcherRunStore using database/sql (SQLite).
type watcherRunStore struct {
	db *sql.DB
}

// NewWatcherRunStore creates a new WatcherRunStore backed by SQLite.
func NewWatcherRunStore(db *sql.DB) WatcherRunStore {
	return &watcherRunStore{db: db}
}

func (s *watcherRunStore) Create(ctx context.Context, watcherID uuid.UUID) (*WatcherRun, error) {
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watcher_runs (id, watcher_id, started_at, created_at)
		 VALUES (?, ?, ?, ?)`,
		id.String(), watcherID.String(), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating watcher run: %w", err)
	}

	return s.GetByID(ctx, id)
}

func (s *watcherRunStore) Complete(ctx context.Context, id uuid.UUID, summary string, details any, hasAlert bool) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var detailsJSON []byte
	if details != nil {
		var err error
		detailsJSON, err = json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshaling run details: %w", err)
		}
	}

	var hasAlertInt int
	if hasAlert {
		hasAlertInt = 1
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE watcher_runs
		 SET finished_at = ?, status = 'completed', summary = ?, details = ?, has_alert = ?
		 WHERE id = ?`,
		now, summary, string(detailsJSON), hasAlertInt, id.String(),
	)
	if err != nil {
		return fmt.Errorf("completing watcher run: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *watcherRunStore) Fail(ctx context.Context, id uuid.UUID, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.db.ExecContext(ctx,
		`UPDATE watcher_runs
		 SET finished_at = ?, status = 'error', error_message = ?
		 WHERE id = ?`,
		now, errMsg, id.String(),
	)
	if err != nil {
		return fmt.Errorf("failing watcher run: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *watcherRunStore) FailStaleRuns(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.db.ExecContext(ctx,
		`UPDATE watcher_runs
		 SET finished_at = ?, status = 'error', error_message = 'stale run cleaned up on startup'
		 WHERE status = 'running' AND started_at < ?`,
		now, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("failing stale runs: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

func (s *watcherRunStore) List(ctx context.Context, watcherID uuid.UUID, limit int) ([]WatcherRun, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, watcher_id, started_at, finished_at, status, summary, details, has_alert, error_message, created_at
		 FROM watcher_runs
		 WHERE watcher_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		watcherID.String(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing watcher runs: %w", err)
	}
	defer rows.Close()

	result := make([]WatcherRun, 0)
	for rows.Next() {
		r, err := scanWatcherRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *r)
	}

	return result, rows.Err()
}

func (s *watcherRunStore) GetByID(ctx context.Context, id uuid.UUID) (*WatcherRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, watcher_id, started_at, finished_at, status, summary, details, has_alert, error_message, created_at
		 FROM watcher_runs WHERE id = ?`, id.String(),
	)

	r := &WatcherRun{}
	var startedAt, createdAt string
	var finishedAt sql.NullString
	var detailsStr sql.NullString
	var hasAlertInt int

	err := row.Scan(
		&r.ID, &r.WatcherID, &startedAt, &finishedAt,
		&r.Status, &r.Summary, &detailsStr, &hasAlertInt, &r.Error, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying watcher run: %w", err)
	}

	r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	r.HasAlert = hasAlertInt != 0
	if finishedAt.Valid {
		t, _ := time.Parse(time.RFC3339, finishedAt.String)
		r.FinishedAt = &t
	}
	if detailsStr.Valid && detailsStr.String != "" {
		r.Details = json.RawMessage(detailsStr.String)
	}

	return r, nil
}

// scanWatcherRun scans a WatcherRun from a rows iterator.
func scanWatcherRun(rows *sql.Rows) (*WatcherRun, error) {
	r := &WatcherRun{}
	var startedAt, createdAt string
	var finishedAt sql.NullString
	var detailsStr sql.NullString
	var hasAlertInt int

	if err := rows.Scan(
		&r.ID, &r.WatcherID, &startedAt, &finishedAt,
		&r.Status, &r.Summary, &detailsStr, &hasAlertInt, &r.Error, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("scanning watcher run: %w", err)
	}

	r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	r.HasAlert = hasAlertInt != 0
	if finishedAt.Valid {
		t, _ := time.Parse(time.RFC3339, finishedAt.String)
		r.FinishedAt = &t
	}
	if detailsStr.Valid && detailsStr.String != "" {
		r.Details = json.RawMessage(detailsStr.String)
	}

	return r, nil
}
