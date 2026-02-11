package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// digestStore implements DigestStore using database/sql (SQLite).
type digestStore struct {
	db *sql.DB
}

// NewDigestStore creates a new DigestStore backed by SQLite.
func NewDigestStore(db *sql.DB) DigestStore {
	return &digestStore{db: db}
}

func (s *digestStore) Save(ctx context.Context, d SaveDigestParams) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO digests (id, environment, status, period_start, period_end,
		 alert_total, alert_critical, alert_warning, watcher_total, watcher_errored,
		 failed_runs, data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Environment, d.Status,
		d.PeriodStart.UTC().Format(time.RFC3339),
		d.PeriodEnd.UTC().Format(time.RFC3339),
		d.AlertTotal, d.AlertCritical, d.AlertWarning,
		d.WatcherTotal, d.WatcherErrored, d.FailedRuns,
		d.Data, now,
	)
	if err != nil {
		return fmt.Errorf("saving digest: %w", err)
	}
	return nil
}

func (s *digestStore) GetLatest(ctx context.Context, environment string) (*StoredDigest, error) {
	query := `SELECT id, environment, status, period_start, period_end,
	          alert_total, alert_critical, alert_warning, watcher_total, watcher_errored,
	          failed_runs, data, created_at
	          FROM digests WHERE 1=1`
	var args []any

	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}

	query += ` ORDER BY period_end DESC LIMIT 1`

	d := &StoredDigest{}
	var periodStart, periodEnd, createdAt string

	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&d.ID, &d.Environment, &d.Status, &periodStart, &periodEnd,
		&d.AlertTotal, &d.AlertCritical, &d.AlertWarning,
		&d.WatcherTotal, &d.WatcherErrored, &d.FailedRuns,
		&d.Data, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying latest digest: %w", err)
	}

	d.PeriodStart, _ = time.Parse(time.RFC3339, periodStart)
	d.PeriodEnd, _ = time.Parse(time.RFC3339, periodEnd)
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

	return d, nil
}

func (s *digestStore) List(ctx context.Context, limit int, environment string) ([]StoredDigest, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `SELECT id, environment, status, period_start, period_end,
	          alert_total, alert_critical, alert_warning, watcher_total, watcher_errored,
	          failed_runs, data, created_at
	          FROM digests WHERE 1=1`
	var args []any

	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}

	query += ` ORDER BY period_end DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing digests: %w", err)
	}
	defer rows.Close()

	result := make([]StoredDigest, 0)
	for rows.Next() {
		var d StoredDigest
		var periodStart, periodEnd, createdAt string

		if err := rows.Scan(
			&d.ID, &d.Environment, &d.Status, &periodStart, &periodEnd,
			&d.AlertTotal, &d.AlertCritical, &d.AlertWarning,
			&d.WatcherTotal, &d.WatcherErrored, &d.FailedRuns,
			&d.Data, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning digest: %w", err)
		}

		d.PeriodStart, _ = time.Parse(time.RFC3339, periodStart)
		d.PeriodEnd, _ = time.Parse(time.RFC3339, periodEnd)
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

		result = append(result, d)
	}

	return result, rows.Err()
}

func (s *digestStore) DeleteOlderThan(ctx context.Context, before time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM digests WHERE created_at < ?`,
		before.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("deleting old digests: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
