package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgAlertStore implements AlertStore using pgx.
type PgAlertStore struct {
	pool *pgxpool.Pool
}

// NewPgAlertStore creates a new PgAlertStore.
func NewPgAlertStore(pool *pgxpool.Pool) *PgAlertStore {
	return &PgAlertStore{pool: pool}
}

func (s *PgAlertStore) Create(ctx context.Context, params CreateAlertParams) (*Alert, error) {
	severity := params.Severity
	if severity == "" {
		severity = SeverityWarning
	}

	a := &Alert{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO alerts (watcher_id, run_id, title, summary, severity, details)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, watcher_id, run_id, title, summary, severity, details, read, dismissed, created_at`,
		params.WatcherID, params.RunID, params.Title, params.Summary, severity, params.Details,
	).Scan(
		&a.ID, &a.WatcherID, &a.RunID, &a.Title, &a.Summary,
		&a.Severity, &a.Details, &a.Read, &a.Dismissed, &a.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating alert: %w", err)
	}

	return a, nil
}

func (s *PgAlertStore) List(ctx context.Context, params ListAlertParams) ([]Alert, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, watcher_id, run_id, title, summary, severity, details, read, dismissed, created_at
		 FROM alerts WHERE 1=1`
	args := []any{}
	argIdx := 1

	if params.UnreadOnly {
		query += fmt.Sprintf(` AND read = false AND dismissed = false`)
	}

	if params.WatcherID != nil {
		query += fmt.Sprintf(` AND watcher_id = $%d`, argIdx)
		args = append(args, *params.WatcherID)
		argIdx++
	}

	query += ` ORDER BY created_at DESC`
	query += fmt.Sprintf(` LIMIT $%d`, argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing alerts: %w", err)
	}
	defer rows.Close()

	result := make([]Alert, 0)
	for rows.Next() {
		var a Alert
		if err := rows.Scan(
			&a.ID, &a.WatcherID, &a.RunID, &a.Title, &a.Summary,
			&a.Severity, &a.Details, &a.Read, &a.Dismissed, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning alert: %w", err)
		}
		result = append(result, a)
	}

	return result, rows.Err()
}

func (s *PgAlertStore) CountUnread(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM alerts WHERE read = false AND dismissed = false`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting unread alerts: %w", err)
	}
	return count, nil
}

func (s *PgAlertStore) MarkRead(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `UPDATE alerts SET read = true WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("marking alert read: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgAlertStore) Dismiss(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `UPDATE alerts SET dismissed = true WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("dismissing alert: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
