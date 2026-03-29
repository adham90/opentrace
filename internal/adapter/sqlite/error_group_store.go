package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type errorGroupStore struct {
	db *bun.DB
}

// NewErrorGroupStore creates a new ErrorGroupStore backed by SQLite.
func NewErrorGroupStore(db *bun.DB) store.ErrorGroupStore {
	return &errorGroupStore{db: db}
}

// Upsert inserts or updates an error group from an ingested log entry.
// If the fingerprint already exists AND is resolved, it reopens the group.
func (s *errorGroupStore) Upsert(ctx context.Context, entry store.LogEntry) error {
	if entry.ErrorFingerprint == "" {
		return nil
	}
	level := strings.ToUpper(entry.Level)
	if level != "ERROR" && level != "FATAL" {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ts := entry.Timestamp.UTC().Format(time.RFC3339)

	// Use NULL for last_log_id when the log entry ID is zero (not yet persisted).
	var lastLogID sql.NullInt64
	if entry.ID > 0 {
		lastLogID = sql.NullInt64{Int64: entry.ID, Valid: true}
	}

	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Check if error group exists and its current status.
		var existingStatus string
		err := tx.NewRaw(`SELECT status FROM error_groups WHERE fingerprint = ?`, entry.ErrorFingerprint).Scan(ctx, &existingStatus)
		if err == sql.ErrNoRows {
			// New error group.
			_, err = tx.NewRaw(`
				INSERT INTO error_groups (fingerprint, service, environment, exception_class, message,
					source_file, source_line, first_seen_at, last_seen_at, last_log_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				entry.ErrorFingerprint, entry.Service, entry.Environment,
				entry.ExceptionClass, truncate(entry.Message, 500),
				entry.SourceFile, entry.SourceLine, ts, ts, lastLogID,
			).Exec(ctx)
			if err != nil {
				return fmt.Errorf("insert error group: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("check existing: %w", err)
		}

		// Existing error group -- update counts.
		_, err = tx.NewRaw(`
			UPDATE error_groups SET
				occurrence_count = occurrence_count + 1,
				last_seen_at = ?,
				last_log_id = ?
			WHERE fingerprint = ?`, ts, lastLogID, entry.ErrorFingerprint,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("update error group: %w", err)
		}

		// Reopen if it was resolved (not ignored -- ignored stays ignored).
		if existingStatus == string(store.ErrorGroupResolved) {
			_, err = tx.NewRaw(`
				UPDATE error_groups SET status = 'unresolved', reopened_count = reopened_count + 1
				WHERE fingerprint = ?`, entry.ErrorFingerprint,
			).Exec(ctx)
			if err != nil {
				return fmt.Errorf("reopen error group: %w", err)
			}

			_, err = tx.NewRaw(`
				INSERT INTO error_group_events (fingerprint, action, reason, created_at)
				VALUES (?, ?, ?, ?)`,
				entry.ErrorFingerprint, "reopened", "New occurrence after resolution", now,
			).Exec(ctx)
			if err != nil {
				return fmt.Errorf("insert reopen event: %w", err)
			}
		}

		return nil
	})
}

func (s *errorGroupStore) Get(ctx context.Context, fingerprint string) (*store.ErrorGroup, error) {
	var eg errorGroupRow
	err := s.db.NewRaw(`
		SELECT fingerprint, service, environment, exception_class, message,
			source_file, source_line, status, first_seen_at, last_seen_at,
			occurrence_count, last_log_id, reopened_count, resolved_at, ignored_at,
			unique_users, impact_score, COALESCE(common_context, '{}')
		FROM error_groups WHERE fingerprint = ?`, fingerprint,
	).Scan(ctx,
		&eg.Fingerprint, &eg.Service, &eg.Environment, &eg.ExceptionClass, &eg.Message,
		&eg.SourceFile, &eg.SourceLine, &eg.Status, &eg.FirstSeenAt, &eg.LastSeenAt,
		&eg.OccurrenceCount, &eg.LastLogID, &eg.ReopenedCount, &eg.ResolvedAt, &eg.IgnoredAt,
		&eg.UniqueUsers, &eg.ImpactScore, &eg.CommonContext,
	)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return eg.toStore(), nil
}

func (s *errorGroupStore) List(ctx context.Context, params store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	var conditions []string
	var args []any

	if params.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, string(params.Status))
	}
	if params.Service != "" {
		conditions = append(conditions, "service = ?")
		args = append(args, params.Service)
	}
	if params.Environment != "" {
		conditions = append(conditions, "environment = ?")
		args = append(args, params.Environment)
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

	query := `SELECT fingerprint, service, environment, exception_class, message,
		source_file, source_line, status, first_seen_at, last_seen_at,
		occurrence_count, last_log_id, reopened_count, resolved_at, ignored_at,
		unique_users, impact_score, COALESCE(common_context, '{}')
		FROM error_groups`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY " + orderBy
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, params.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list error groups: %w", err)
	}
	defer rows.Close()

	var groups []store.ErrorGroup
	for rows.Next() {
		var eg errorGroupRow
		if err := rows.Scan(
			&eg.Fingerprint, &eg.Service, &eg.Environment, &eg.ExceptionClass, &eg.Message,
			&eg.SourceFile, &eg.SourceLine, &eg.Status, &eg.FirstSeenAt, &eg.LastSeenAt,
			&eg.OccurrenceCount, &eg.LastLogID, &eg.ReopenedCount, &eg.ResolvedAt, &eg.IgnoredAt,
			&eg.UniqueUsers, &eg.ImpactScore, &eg.CommonContext,
		); err != nil {
			return nil, err
		}
		groups = append(groups, *eg.toStore())
	}
	return groups, rows.Err()
}

func (s *errorGroupStore) Count(ctx context.Context, status store.ErrorGroupStatus) (int, error) {
	var n int
	if status != "" {
		err := s.db.NewRaw(`SELECT COUNT(*) FROM error_groups WHERE status = ?`, string(status)).Scan(ctx, &n)
		return n, err
	}
	err := s.db.NewRaw(`SELECT COUNT(*) FROM error_groups`).Scan(ctx, &n)
	return n, err
}

func (s *errorGroupStore) Resolve(ctx context.Context, fingerprint string, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewRaw(`
			UPDATE error_groups SET status = 'resolved', resolved_at = ?
			WHERE fingerprint = ? AND status != 'resolved'`,
			now, fingerprint,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("resolve: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return store.ErrNotFound
		}

		_, err = tx.NewRaw(`
			INSERT INTO error_group_events (fingerprint, action, reason, created_at)
			VALUES (?, ?, ?, ?)`,
			fingerprint, "resolved", reason, now,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("insert resolve event: %w", err)
		}

		return nil
	})
}

func (s *errorGroupStore) Ignore(ctx context.Context, fingerprint string, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewRaw(`
			UPDATE error_groups SET status = 'ignored', ignored_at = ?
			WHERE fingerprint = ? AND status != 'ignored'`,
			now, fingerprint,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("ignore: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return store.ErrNotFound
		}

		_, err = tx.NewRaw(`
			INSERT INTO error_group_events (fingerprint, action, reason, created_at)
			VALUES (?, ?, ?, ?)`,
			fingerprint, "ignored", reason, now,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("insert ignore event: %w", err)
		}

		return nil
	})
}

func (s *errorGroupStore) ListEvents(ctx context.Context, fingerprint string, limit int) ([]store.ErrorGroupEvent, error) {
	if limit <= 0 {
		limit = 20
	}

	type row struct {
		Fingerprint string `bun:"fingerprint"`
		Action      string `bun:"action"`
		Reason      string `bun:"reason"`
		CreatedAt   string `bun:"created_at"`
	}

	var rows []row
	err := s.db.NewRaw(`
		SELECT fingerprint, action, reason, created_at
		FROM error_group_events
		WHERE fingerprint = ?
		ORDER BY created_at DESC
		LIMIT ?`, fingerprint, limit,
	).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	events := make([]store.ErrorGroupEvent, len(rows))
	for i, r := range rows {
		events[i] = store.ErrorGroupEvent{
			Fingerprint: r.Fingerprint,
			Action:      r.Action,
			Reason:      r.Reason,
			CreatedAt:   parseTime(r.CreatedAt),
		}
	}
	return events, nil
}

func (s *errorGroupStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UTC().Format(time.RFC3339)
	res, err := s.db.NewRaw(`DELETE FROM error_groups WHERE last_seen_at < ?`, cutoff).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// errorGroupRow is a scan helper for error group queries.
type errorGroupRow struct {
	Fingerprint     string
	Service         string
	Environment     string
	ExceptionClass  string
	Message         string
	SourceFile      string
	SourceLine      int
	Status          string
	FirstSeenAt     string
	LastSeenAt      string
	OccurrenceCount int
	LastLogID       sql.NullInt64
	ReopenedCount   int
	ResolvedAt      sql.NullString
	IgnoredAt       sql.NullString
	UniqueUsers     int
	ImpactScore     float64
	CommonContext   string
}

func (r *errorGroupRow) toStore() *store.ErrorGroup {
	eg := &store.ErrorGroup{
		Fingerprint:     r.Fingerprint,
		Service:         r.Service,
		Environment:     r.Environment,
		ExceptionClass:  r.ExceptionClass,
		Message:         r.Message,
		SourceFile:      r.SourceFile,
		SourceLine:      r.SourceLine,
		Status:          store.ErrorGroupStatus(r.Status),
		FirstSeenAt:     parseTime(r.FirstSeenAt),
		LastSeenAt:      parseTime(r.LastSeenAt),
		OccurrenceCount: r.OccurrenceCount,
		ReopenedCount:   r.ReopenedCount,
		UniqueUsers:     r.UniqueUsers,
		ImpactScore:     r.ImpactScore,
	}
	if r.LastLogID.Valid {
		eg.LastLogID = &r.LastLogID.Int64
	}
	if r.ResolvedAt.Valid {
		t, _ := time.Parse(time.RFC3339, r.ResolvedAt.String)
		eg.ResolvedAt = &t
	}
	if r.IgnoredAt.Valid {
		t, _ := time.Parse(time.RFC3339, r.IgnoredAt.String)
		eg.IgnoredAt = &t
	}
	if r.CommonContext != "" && r.CommonContext != "{}" {
		json.Unmarshal([]byte(r.CommonContext), &eg.CommonContext)
	}
	return eg
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
