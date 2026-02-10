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

// alertStore implements AlertStore using database/sql (SQLite).
type alertStore struct {
	db *sql.DB
}

// NewAlertStore creates a new AlertStore backed by SQLite.
func NewAlertStore(db *sql.DB) AlertStore {
	return &alertStore{db: db}
}

func (s *alertStore) Create(ctx context.Context, params CreateAlertParams) (*Alert, error) {
	severity := params.Severity
	if severity == "" {
		severity = SeverityWarning
	}

	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)

	var watcherIDStr, runIDStr *string
	if params.WatcherID != nil {
		s := params.WatcherID.String()
		watcherIDStr = &s
	}
	if params.RunID != nil {
		s := params.RunID.String()
		runIDStr = &s
	}

	var detailsStr *string
	if params.Details != nil {
		s := string(params.Details)
		detailsStr = &s
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alerts (id, watcher_id, run_id, title, summary, severity, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), watcherIDStr, runIDStr, params.Title, params.Summary,
		string(severity), detailsStr, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating alert: %w", err)
	}

	return s.getByID(ctx, id)
}

func (s *alertStore) getByID(ctx context.Context, id uuid.UUID) (*Alert, error) {
	a := &Alert{}
	var createdAt string
	var detailsStr sql.NullString
	var readInt, dismissedInt int

	err := s.db.QueryRowContext(ctx,
		`SELECT id, watcher_id, run_id, title, summary, severity, details, read, dismissed, created_at
		 FROM alerts WHERE id = ?`, id.String(),
	).Scan(
		&a.ID, &a.WatcherID, &a.RunID, &a.Title, &a.Summary,
		&a.Severity, &detailsStr, &readInt, &dismissedInt, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying alert: %w", err)
	}

	a.Read = readInt != 0
	a.Dismissed = dismissedInt != 0
	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if detailsStr.Valid && detailsStr.String != "" {
		a.Details = json.RawMessage(detailsStr.String)
	}

	return a, nil
}

func (s *alertStore) List(ctx context.Context, params ListAlertParams) ([]Alert, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, watcher_id, run_id, title, summary, severity, details, read, dismissed, created_at
		 FROM alerts WHERE 1=1`
	var args []any

	if params.UnreadOnly {
		query += ` AND read = 0 AND dismissed = 0`
	}

	if params.WatcherID != nil {
		query += ` AND watcher_id = ?`
		args = append(args, params.WatcherID.String())
	}

	query += ` ORDER BY created_at DESC`
	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing alerts: %w", err)
	}
	defer rows.Close()

	result := make([]Alert, 0)
	for rows.Next() {
		var a Alert
		var createdAt string
		var detailsStr sql.NullString
		var readInt, dismissedInt int

		if err := rows.Scan(
			&a.ID, &a.WatcherID, &a.RunID, &a.Title, &a.Summary,
			&a.Severity, &detailsStr, &readInt, &dismissedInt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning alert: %w", err)
		}

		a.Read = readInt != 0
		a.Dismissed = dismissedInt != 0
		a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		if detailsStr.Valid && detailsStr.String != "" {
			a.Details = json.RawMessage(detailsStr.String)
		}

		result = append(result, a)
	}

	return result, rows.Err()
}

func (s *alertStore) CountUnread(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts WHERE read = 0 AND dismissed = 0`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting unread alerts: %w", err)
	}
	return count, nil
}

func (s *alertStore) MarkRead(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `UPDATE alerts SET read = 1 WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("marking alert read: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *alertStore) Dismiss(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `UPDATE alerts SET dismissed = 1 WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("dismissing alert: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *alertStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM alerts WHERE created_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning alerts: %w", err)
	}
	return result.RowsAffected()
}
