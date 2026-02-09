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

// watcherStore implements WatcherStore using database/sql (SQLite).
type watcherStore struct {
	db *sql.DB
}

// NewWatcherStore creates a new WatcherStore backed by SQLite.
func NewWatcherStore(db *sql.DB) WatcherStore {
	return &watcherStore{db: db}
}

func (s *watcherStore) Create(ctx context.Context, params CreateWatcherParams) (*Watcher, error) {
	severity := params.Severity
	if severity == "" {
		severity = SeverityWarning
	}
	timeRange := params.TimeRange
	if timeRange == "" {
		timeRange = "15m"
	}
	filters := params.Filters
	if filters == nil {
		filters = json.RawMessage(`{}`)
	}
	notify := params.Notify
	if notify == nil {
		notify = json.RawMessage(`["dashboard"]`)
	}

	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watchers (id, title, description, severity, filters, time_range, model, notify, next_run_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), params.Title, params.Description, string(severity),
		string(filters), timeRange, params.Model, string(notify), now, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting watcher: %w", err)
	}

	return s.GetByID(ctx, id)
}

func (s *watcherStore) GetByID(ctx context.Context, id uuid.UUID) (*Watcher, error) {
	w := &Watcher{}
	var filtersStr, notifyStr string
	var createdAt, updatedAt string
	var lastRunAt, nextRunAt sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, description, severity, filters, time_range, model, status, notify,
		        last_run_at, next_run_at, last_error, created_at, updated_at
		 FROM watchers WHERE id = ?`, id.String(),
	).Scan(
		&w.ID, &w.Title, &w.Description, &w.Severity, &filtersStr,
		&w.TimeRange, &w.Model, &w.Status, &notifyStr,
		&lastRunAt, &nextRunAt, &w.LastError,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying watcher: %w", err)
	}

	w.Filters = json.RawMessage(filtersStr)
	w.Notify = json.RawMessage(notifyStr)
	w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastRunAt.Valid {
		t, _ := time.Parse(time.RFC3339, lastRunAt.String)
		w.LastRunAt = &t
	}
	if nextRunAt.Valid {
		t, _ := time.Parse(time.RFC3339, nextRunAt.String)
		w.NextRunAt = &t
	}

	return w, nil
}

func (s *watcherStore) List(ctx context.Context) ([]Watcher, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, description, severity, filters, time_range, model, status, notify,
		        last_run_at, next_run_at, last_error, created_at, updated_at
		 FROM watchers ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying watchers: %w", err)
	}
	defer rows.Close()

	result := make([]Watcher, 0)
	for rows.Next() {
		var w Watcher
		var filtersStr, notifyStr string
		var createdAt, updatedAt string
		var lastRunAt, nextRunAt sql.NullString

		if err := rows.Scan(
			&w.ID, &w.Title, &w.Description, &w.Severity, &filtersStr,
			&w.TimeRange, &w.Model, &w.Status, &notifyStr,
			&lastRunAt, &nextRunAt, &w.LastError,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning watcher: %w", err)
		}

		w.Filters = json.RawMessage(filtersStr)
		w.Notify = json.RawMessage(notifyStr)
		w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if lastRunAt.Valid {
			t, _ := time.Parse(time.RFC3339, lastRunAt.String)
			w.LastRunAt = &t
		}
		if nextRunAt.Valid {
			t, _ := time.Parse(time.RFC3339, nextRunAt.String)
			w.NextRunAt = &t
		}

		result = append(result, w)
	}

	return result, rows.Err()
}

func (s *watcherStore) Update(ctx context.Context, id uuid.UUID, params UpdateWatcherParams) (*Watcher, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var titleStr, descStr *string
	var sevStr *string
	var filtersStr, timeRangeStr, modelStr, notifyStr *string
	if params.Title != nil {
		titleStr = params.Title
	}
	if params.Description != nil {
		descStr = params.Description
	}
	if params.Severity != nil {
		s := string(*params.Severity)
		sevStr = &s
	}
	if params.Filters != nil {
		s := string(params.Filters)
		filtersStr = &s
	}
	if params.TimeRange != nil {
		timeRangeStr = params.TimeRange
	}
	if params.Model != nil {
		modelStr = params.Model
	}
	if params.Notify != nil {
		s := string(params.Notify)
		notifyStr = &s
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers
		 SET title       = COALESCE(?, title),
		     description = COALESCE(?, description),
		     severity    = COALESCE(?, severity),
		     filters     = COALESCE(?, filters),
		     time_range  = COALESCE(?, time_range),
		     model       = COALESCE(?, model),
		     notify      = COALESCE(?, notify),
		     updated_at  = ?
		 WHERE id = ?`,
		titleStr, descStr, sevStr, filtersStr, timeRangeStr, modelStr, notifyStr, now, id.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("updating watcher: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}

	return s.GetByID(ctx, id)
}

func (s *watcherStore) UpdateStatus(ctx context.Context, id uuid.UUID, status WatcherStatus) (*Watcher, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), now, id.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("updating watcher status: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.GetByID(ctx, id)
}

func (s *watcherStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM watchers WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("deleting watcher: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *watcherStore) GetDueWatchers(ctx context.Context) ([]Watcher, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, description, severity, filters, time_range, model, status, notify,
		        last_run_at, next_run_at, last_error, created_at, updated_at
		 FROM watchers
		 WHERE status = 'active' AND next_run_at <= ?
		 ORDER BY next_run_at ASC`, now,
	)
	if err != nil {
		return nil, fmt.Errorf("querying due watchers: %w", err)
	}
	defer rows.Close()

	result := make([]Watcher, 0)
	for rows.Next() {
		var w Watcher
		var filtersStr, notifyStr string
		var createdAt, updatedAt string
		var lastRunAt, nextRunAt sql.NullString

		if err := rows.Scan(
			&w.ID, &w.Title, &w.Description, &w.Severity, &filtersStr,
			&w.TimeRange, &w.Model, &w.Status, &notifyStr,
			&lastRunAt, &nextRunAt, &w.LastError,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning due watcher: %w", err)
		}

		w.Filters = json.RawMessage(filtersStr)
		w.Notify = json.RawMessage(notifyStr)
		w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if lastRunAt.Valid {
			t, _ := time.Parse(time.RFC3339, lastRunAt.String)
			w.LastRunAt = &t
		}
		if nextRunAt.Valid {
			t, _ := time.Parse(time.RFC3339, nextRunAt.String)
			w.NextRunAt = &t
		}

		result = append(result, w)
	}

	return result, rows.Err()
}

func (s *watcherStore) UpdateRunTime(ctx context.Context, id uuid.UUID, lastRun, nextRun time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers SET last_run_at = ?, next_run_at = ?, updated_at = ? WHERE id = ?`,
		lastRun.UTC().Format(time.RFC3339), nextRun.UTC().Format(time.RFC3339), now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("updating watcher run time: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
