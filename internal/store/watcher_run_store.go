package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgWatcherRunStore implements WatcherRunStore using pgx.
type PgWatcherRunStore struct {
	pool *pgxpool.Pool
}

// NewPgWatcherRunStore creates a new PgWatcherRunStore.
func NewPgWatcherRunStore(pool *pgxpool.Pool) *PgWatcherRunStore {
	return &PgWatcherRunStore{pool: pool}
}

func (s *PgWatcherRunStore) Create(ctx context.Context, watcherID uuid.UUID) (*WatcherRun, error) {
	r := &WatcherRun{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO watcher_runs (watcher_id)
		 VALUES ($1)
		 RETURNING id, watcher_id, started_at, finished_at, status, summary, details, has_alert, error, created_at`,
		watcherID,
	).Scan(
		&r.ID, &r.WatcherID, &r.StartedAt, &r.FinishedAt,
		&r.Status, &r.Summary, &r.Details, &r.HasAlert, &r.Error, &r.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating watcher run: %w", err)
	}

	return r, nil
}

func (s *PgWatcherRunStore) Complete(ctx context.Context, id uuid.UUID, summary string, details any, hasAlert bool) error {
	now := time.Now()

	var detailsJSON []byte
	if details != nil {
		var err error
		detailsJSON, err = json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshaling run details: %w", err)
		}
	}

	result, err := s.pool.Exec(ctx,
		`UPDATE watcher_runs
		 SET finished_at = $2, status = 'completed', summary = $3, details = $4, has_alert = $5
		 WHERE id = $1`,
		id, now, summary, detailsJSON, hasAlert,
	)
	if err != nil {
		return fmt.Errorf("completing watcher run: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgWatcherRunStore) Fail(ctx context.Context, id uuid.UUID, errMsg string) error {
	now := time.Now()

	result, err := s.pool.Exec(ctx,
		`UPDATE watcher_runs
		 SET finished_at = $2, status = 'error', error = $3
		 WHERE id = $1`,
		id, now, errMsg,
	)
	if err != nil {
		return fmt.Errorf("failing watcher run: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgWatcherRunStore) List(ctx context.Context, watcherID uuid.UUID, limit int) ([]WatcherRun, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, watcher_id, started_at, finished_at, status, summary, details, has_alert, error, created_at
		 FROM watcher_runs
		 WHERE watcher_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		watcherID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing watcher runs: %w", err)
	}
	defer rows.Close()

	result := make([]WatcherRun, 0)
	for rows.Next() {
		var r WatcherRun
		if err := rows.Scan(
			&r.ID, &r.WatcherID, &r.StartedAt, &r.FinishedAt,
			&r.Status, &r.Summary, &r.Details, &r.HasAlert, &r.Error, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning watcher run: %w", err)
		}
		result = append(result, r)
	}

	return result, rows.Err()
}

func (s *PgWatcherRunStore) GetByID(ctx context.Context, id uuid.UUID) (*WatcherRun, error) {
	r := &WatcherRun{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, watcher_id, started_at, finished_at, status, summary, details, has_alert, error, created_at
		 FROM watcher_runs WHERE id = $1`, id,
	).Scan(
		&r.ID, &r.WatcherID, &r.StartedAt, &r.FinishedAt,
		&r.Status, &r.Summary, &r.Details, &r.HasAlert, &r.Error, &r.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying watcher run: %w", err)
	}

	return r, nil
}
