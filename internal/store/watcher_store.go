package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgWatcherStore implements WatcherStore using pgx.
type PgWatcherStore struct {
	pool *pgxpool.Pool
}

// NewPgWatcherStore creates a new PgWatcherStore.
func NewPgWatcherStore(pool *pgxpool.Pool) *PgWatcherStore {
	return &PgWatcherStore{pool: pool}
}

func (s *PgWatcherStore) Create(ctx context.Context, params CreateWatcherParams) (*Watcher, error) {
	// Default severity
	severity := params.Severity
	if severity == "" {
		severity = SeverityWarning
	}

	// Default interval
	interval := params.IntervalSeconds
	if interval <= 0 {
		interval = 300
	}

	// Default filters
	filters := params.Filters
	if filters == nil {
		filters = []byte(`{}`)
	}

	// Default notify
	notify := params.Notify
	if notify == nil {
		notify = []byte(`["dashboard"]`)
	}

	// Set initial next_run_at to now so it runs immediately
	nextRun := time.Now()

	w := &Watcher{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO watchers (title, description, severity, filters, interval_seconds, notify, next_run_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, title, description, severity, filters, interval_seconds, status, notify,
		           last_run_at, next_run_at, last_error, created_at, updated_at`,
		params.Title, params.Description, severity, filters, interval, notify, nextRun,
	).Scan(
		&w.ID, &w.Title, &w.Description, &w.Severity, &w.Filters,
		&w.IntervalSeconds, &w.Status, &w.Notify,
		&w.LastRunAt, &w.NextRunAt, &w.LastError,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting watcher: %w", err)
	}

	return w, nil
}

func (s *PgWatcherStore) GetByID(ctx context.Context, id uuid.UUID) (*Watcher, error) {
	w := &Watcher{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, title, description, severity, filters, interval_seconds, status, notify,
		        last_run_at, next_run_at, last_error, created_at, updated_at
		 FROM watchers WHERE id = $1`, id,
	).Scan(
		&w.ID, &w.Title, &w.Description, &w.Severity, &w.Filters,
		&w.IntervalSeconds, &w.Status, &w.Notify,
		&w.LastRunAt, &w.NextRunAt, &w.LastError,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying watcher: %w", err)
	}

	return w, nil
}

func (s *PgWatcherStore) List(ctx context.Context) ([]Watcher, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, title, description, severity, filters, interval_seconds, status, notify,
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
		if err := rows.Scan(
			&w.ID, &w.Title, &w.Description, &w.Severity, &w.Filters,
			&w.IntervalSeconds, &w.Status, &w.Notify,
			&w.LastRunAt, &w.NextRunAt, &w.LastError,
			&w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning watcher: %w", err)
		}
		result = append(result, w)
	}

	return result, rows.Err()
}

func (s *PgWatcherStore) Update(ctx context.Context, id uuid.UUID, params UpdateWatcherParams) (*Watcher, error) {
	now := time.Now()
	w := &Watcher{}

	err := s.pool.QueryRow(ctx,
		`UPDATE watchers
		 SET title            = COALESCE($2, title),
		     description      = COALESCE($3, description),
		     severity         = COALESCE($4, severity),
		     filters          = COALESCE($5, filters),
		     interval_seconds = COALESCE($6, interval_seconds),
		     notify           = COALESCE($7, notify),
		     updated_at       = $8
		 WHERE id = $1
		 RETURNING id, title, description, severity, filters, interval_seconds, status, notify,
		           last_run_at, next_run_at, last_error, created_at, updated_at`,
		id, params.Title, params.Description, params.Severity,
		params.Filters, params.IntervalSeconds, params.Notify, now,
	).Scan(
		&w.ID, &w.Title, &w.Description, &w.Severity, &w.Filters,
		&w.IntervalSeconds, &w.Status, &w.Notify,
		&w.LastRunAt, &w.NextRunAt, &w.LastError,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating watcher: %w", err)
	}

	return w, nil
}

func (s *PgWatcherStore) UpdateStatus(ctx context.Context, id uuid.UUID, status WatcherStatus) (*Watcher, error) {
	w := &Watcher{}
	err := s.pool.QueryRow(ctx,
		`UPDATE watchers SET status = $2, updated_at = now()
		 WHERE id = $1
		 RETURNING id, title, description, severity, filters, interval_seconds, status, notify,
		           last_run_at, next_run_at, last_error, created_at, updated_at`,
		id, status,
	).Scan(
		&w.ID, &w.Title, &w.Description, &w.Severity, &w.Filters,
		&w.IntervalSeconds, &w.Status, &w.Notify,
		&w.LastRunAt, &w.NextRunAt, &w.LastError,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating watcher status: %w", err)
	}
	return w, nil
}

func (s *PgWatcherStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM watchers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting watcher: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgWatcherStore) GetDueWatchers(ctx context.Context) ([]Watcher, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, title, description, severity, filters, interval_seconds, status, notify,
		        last_run_at, next_run_at, last_error, created_at, updated_at
		 FROM watchers
		 WHERE status = 'active' AND next_run_at <= now()
		 ORDER BY next_run_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying due watchers: %w", err)
	}
	defer rows.Close()

	result := make([]Watcher, 0)
	for rows.Next() {
		var w Watcher
		if err := rows.Scan(
			&w.ID, &w.Title, &w.Description, &w.Severity, &w.Filters,
			&w.IntervalSeconds, &w.Status, &w.Notify,
			&w.LastRunAt, &w.NextRunAt, &w.LastError,
			&w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning due watcher: %w", err)
		}
		result = append(result, w)
	}

	return result, rows.Err()
}

func (s *PgWatcherStore) UpdateRunTime(ctx context.Context, id uuid.UUID, lastRun, nextRun time.Time) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE watchers SET last_run_at = $2, next_run_at = $3, updated_at = now() WHERE id = $1`,
		id, lastRun, nextRun,
	)
	if err != nil {
		return fmt.Errorf("updating watcher run time: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
