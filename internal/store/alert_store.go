package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

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
		`INSERT INTO alerts (id, watcher_id, run_id, title, summary, environment, severity, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), watcherIDStr, runIDStr, params.Title, params.Summary,
		params.Environment, string(severity), detailsStr, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("creating alert: %w", err)
	}

	return &Alert{
		ID:          id,
		WatcherID:   params.WatcherID,
		RunID:       params.RunID,
		Title:       params.Title,
		Summary:     params.Summary,
		Environment: params.Environment,
		Severity:    severity,
		Details:     params.Details,
		CreatedAt:   now,
	}, nil
}

func (s *alertStore) GetByID(ctx context.Context, id uuid.UUID) (*Alert, error) {
	a := &Alert{}
	var createdAt string
	var detailsStr sql.NullString
	var readInt, dismissedInt int

	err := s.db.QueryRowContext(ctx,
		`SELECT id, watcher_id, run_id, title, summary, environment, severity, details, read, dismissed, created_at
		 FROM alerts WHERE id = ?`, id.String(),
	).Scan(
		&a.ID, &a.WatcherID, &a.RunID, &a.Title, &a.Summary,
		&a.Environment, &a.Severity, &detailsStr, &readInt, &dismissedInt, &createdAt,
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

	query := `SELECT a.id, a.watcher_id, a.run_id, COALESCE(w.title, ''), a.title, a.summary, a.environment, a.severity, a.details, a.read, a.dismissed, a.created_at
		 FROM alerts a LEFT JOIN watchers w ON a.watcher_id = w.id`
	var conditions []string
	var args []any

	if params.DismissedOnly {
		conditions = append(conditions, `a.dismissed = 1`)
	} else if params.UnreadOnly {
		conditions = append(conditions, `a.read = 0 AND a.dismissed = 0`)
	} else {
		conditions = append(conditions, `a.dismissed = 0`)
	}

	if params.Severity != "" {
		conditions = append(conditions, `a.severity = ?`)
		args = append(args, string(params.Severity))
	}

	if params.WatcherID != nil {
		conditions = append(conditions, `a.watcher_id = ?`)
		args = append(args, params.WatcherID.String())
	}

	if params.Environment != "" {
		conditions = append(conditions, `a.environment = ?`)
		args = append(args, params.Environment)
	}

	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}

	query += ` ORDER BY a.created_at DESC`
	query += ` LIMIT ?`
	args = append(args, limit)

	if params.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, params.Offset)
	}

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
			&a.ID, &a.WatcherID, &a.RunID, &a.WatcherTitle, &a.Title, &a.Summary,
			&a.Environment, &a.Severity, &detailsStr, &readInt, &dismissedInt, &createdAt,
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

func (s *alertStore) CountUnread(ctx context.Context, environment string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM alerts WHERE read = 0 AND dismissed = 0`
	var args []any
	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting unread alerts: %w", err)
	}
	return count, nil
}

func (s *alertStore) CountTotal(ctx context.Context, environment string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM alerts WHERE dismissed = 0`
	var args []any
	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting total alerts: %w", err)
	}
	return count, nil
}

func (s *alertStore) CountBySeverity(ctx context.Context, since, until time.Time, environment string) (map[string]int, error) {
	query := `SELECT severity, COUNT(*) FROM alerts WHERE dismissed = 0 AND created_at >= ? AND created_at < ?`
	args := []any{since.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339)}

	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}

	query += ` GROUP BY severity`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("counting alerts by severity: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var severity string
		var count int
		if err := rows.Scan(&severity, &count); err != nil {
			return nil, fmt.Errorf("scanning severity count: %w", err)
		}
		result[severity] = count
	}
	return result, rows.Err()
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

func (s *alertStore) MarkAllRead(ctx context.Context, environment string) error {
	query := `UPDATE alerts SET read = 1 WHERE read = 0 AND dismissed = 0`
	var args []any
	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("marking all alerts read: %w", err)
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

func (s *alertStore) DismissAll(ctx context.Context, environment string) error {
	query := `UPDATE alerts SET dismissed = 1 WHERE dismissed = 0`
	var args []any
	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("dismissing all alerts: %w", err)
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
