package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type errorGroupStore struct {
	db *sql.DB
}

// NewErrorGroupStore creates a new ErrorGroupStore backed by SQLite.
func NewErrorGroupStore(db *sql.DB) ErrorGroupStore {
	return &errorGroupStore{db: db}
}

// Upsert inserts or updates an error group from an ingested log entry.
// If the fingerprint already exists AND is resolved, it reopens the group.
func (s *errorGroupStore) Upsert(ctx context.Context, entry LogEntry) error {
	if entry.ErrorFingerprint == "" {
		return nil
	}
	level := strings.ToUpper(entry.Level)
	if level != "ERROR" && level != "FATAL" {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ts := entry.Timestamp.UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Check if error group exists and its current status.
	var existingStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM error_groups WHERE fingerprint = ?`,
		entry.ErrorFingerprint,
	).Scan(&existingStatus)

	// Use NULL for last_log_id when the log entry ID is zero (not yet persisted).
	var lastLogID any
	if entry.ID > 0 {
		lastLogID = entry.ID
	}

	if err == sql.ErrNoRows {
		// New error group.
		_, err = tx.ExecContext(ctx,
			`INSERT INTO error_groups (fingerprint, service, environment, exception_class,
				message, source_file, source_line, status, first_seen_at, last_seen_at,
				occurrence_count, last_log_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'unresolved', ?, ?, 1, ?)`,
			entry.ErrorFingerprint, entry.Service, entry.Environment, entry.ExceptionClass,
			truncate(entry.Message, 500), entry.SourceFile, entry.SourceLine,
			ts, ts, lastLogID,
		)
		if err != nil {
			return fmt.Errorf("insert error group: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("check existing: %w", err)
	} else {
		// Existing error group — update counts.
		_, err = tx.ExecContext(ctx,
			`UPDATE error_groups
			 SET occurrence_count = occurrence_count + 1,
			     last_seen_at = ?,
			     last_log_id = ?
			 WHERE fingerprint = ?`,
			ts, lastLogID, entry.ErrorFingerprint,
		)
		if err != nil {
			return fmt.Errorf("update error group: %w", err)
		}

		// Reopen if it was resolved (not ignored — ignored stays ignored).
		if existingStatus == string(ErrorGroupResolved) {
			_, err = tx.ExecContext(ctx,
				`UPDATE error_groups
				 SET status = 'unresolved',
				     reopened_count = reopened_count + 1,
				     resolved_at = NULL
				 WHERE fingerprint = ?`,
				entry.ErrorFingerprint,
			)
			if err != nil {
				return fmt.Errorf("reopen error group: %w", err)
			}
			_, err = tx.ExecContext(ctx,
				`INSERT INTO error_group_events (fingerprint, action, reason, created_at)
				 VALUES (?, 'reopened', 'New occurrence after resolution', ?)`,
				entry.ErrorFingerprint, now,
			)
			if err != nil {
				return fmt.Errorf("insert reopen event: %w", err)
			}
		}
	}

	return tx.Commit()
}

func (s *errorGroupStore) Get(ctx context.Context, fingerprint string) (*ErrorGroup, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT fingerprint, service, environment, exception_class, message,
		        source_file, source_line, status, first_seen_at, last_seen_at,
		        occurrence_count, last_log_id, reopened_count, resolved_at, ignored_at
		 FROM error_groups WHERE fingerprint = ?`, fingerprint)

	eg, err := scanErrorGroup(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return eg, err
}

func (s *errorGroupStore) List(ctx context.Context, params ListErrorGroupParams) ([]ErrorGroup, error) {
	var clauses []string
	var args []any

	if params.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(params.Status))
	}
	if params.Service != "" {
		clauses = append(clauses, "service = ?")
		args = append(args, params.Service)
	}
	if params.Environment != "" {
		clauses = append(clauses, "environment = ?")
		args = append(args, params.Environment)
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	orderBy := "last_seen_at DESC"
	switch params.SortBy {
	case "occurrence_count":
		orderBy = "occurrence_count DESC"
	case "first_seen_at":
		orderBy = "first_seen_at DESC"
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	query := fmt.Sprintf(
		`SELECT fingerprint, service, environment, exception_class, message,
		        source_file, source_line, status, first_seen_at, last_seen_at,
		        occurrence_count, last_log_id, reopened_count, resolved_at, ignored_at
		 FROM error_groups %s ORDER BY %s LIMIT ? OFFSET ?`,
		where, orderBy)
	args = append(args, limit, params.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list error groups: %w", err)
	}
	defer rows.Close()

	var groups []ErrorGroup
	for rows.Next() {
		eg, err := scanErrorGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, *eg)
	}
	return groups, rows.Err()
}

func (s *errorGroupStore) Count(ctx context.Context, status ErrorGroupStatus) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM error_groups`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, string(status))
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *errorGroupStore) Resolve(ctx context.Context, fingerprint string, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE error_groups SET status = 'resolved', resolved_at = ? WHERE fingerprint = ?`,
		now, fingerprint)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO error_group_events (fingerprint, action, reason, created_at)
		 VALUES (?, 'resolved', ?, ?)`,
		fingerprint, reason, now)
	if err != nil {
		return fmt.Errorf("insert resolve event: %w", err)
	}

	return tx.Commit()
}

func (s *errorGroupStore) Ignore(ctx context.Context, fingerprint string, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE error_groups SET status = 'ignored', ignored_at = ? WHERE fingerprint = ?`,
		now, fingerprint)
	if err != nil {
		return fmt.Errorf("ignore: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO error_group_events (fingerprint, action, reason, created_at)
		 VALUES (?, 'ignored', ?, ?)`,
		fingerprint, reason, now)
	if err != nil {
		return fmt.Errorf("insert ignore event: %w", err)
	}

	return tx.Commit()
}

func (s *errorGroupStore) ListEvents(ctx context.Context, fingerprint string, limit int) ([]ErrorGroupEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, fingerprint, action, reason, created_at
		 FROM error_group_events
		 WHERE fingerprint = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		fingerprint, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []ErrorGroupEvent
	for rows.Next() {
		var ev ErrorGroupEvent
		var createdAt string
		if err := rows.Scan(&ev.ID, &ev.Fingerprint, &ev.Action, &ev.Reason, &createdAt); err != nil {
			return nil, err
		}
		ev.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *errorGroupStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM error_groups WHERE last_seen_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// scanErrorGroup scans a row into an ErrorGroup.
func scanErrorGroup(sc interface{ Scan(...any) error }) (*ErrorGroup, error) {
	var eg ErrorGroup
	var firstSeen, lastSeen string
	var resolvedAt, ignoredAt sql.NullString
	var lastLogID sql.NullInt64

	err := sc.Scan(
		&eg.Fingerprint, &eg.Service, &eg.Environment, &eg.ExceptionClass, &eg.Message,
		&eg.SourceFile, &eg.SourceLine, &eg.Status, &firstSeen, &lastSeen,
		&eg.OccurrenceCount, &lastLogID, &eg.ReopenedCount, &resolvedAt, &ignoredAt,
	)
	if err != nil {
		return nil, err
	}

	eg.FirstSeenAt, _ = time.Parse(time.RFC3339, firstSeen)
	eg.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeen)
	if lastLogID.Valid {
		eg.LastLogID = &lastLogID.Int64
	}
	if resolvedAt.Valid {
		t, _ := time.Parse(time.RFC3339, resolvedAt.String)
		eg.ResolvedAt = &t
	}
	if ignoredAt.Valid {
		t, _ := time.Parse(time.RFC3339, ignoredAt.String)
		eg.IgnoredAt = &t
	}

	return &eg, nil
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
