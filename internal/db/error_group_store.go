package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/adham90/opentrace/pkg/store"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Question)

type errorGroupStore struct {
	db *sql.DB
	q  *Queries
}

// NewErrorGroupStore creates a new ErrorGroupStore backed by SQLite.
func NewErrorGroupStore(db *sql.DB) store.ErrorGroupStore {
	return &errorGroupStore{db: db, q: New(db)}
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qtx := s.q.WithTx(tx)

	// Use NULL for last_log_id when the log entry ID is zero (not yet persisted).
	var lastLogID sql.NullInt64
	if entry.ID > 0 {
		lastLogID = sql.NullInt64{Int64: entry.ID, Valid: true}
	}

	// Check if error group exists and its current status.
	existingStatus, err := qtx.GetErrorGroupStatus(ctx, entry.ErrorFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		// New error group.
		err = qtx.InsertErrorGroup(ctx, InsertErrorGroupParams{
			Fingerprint:    entry.ErrorFingerprint,
			Service:        entry.Service,
			Environment:    entry.Environment,
			ExceptionClass: entry.ExceptionClass,
			Message:        truncate(entry.Message, 500),
			SourceFile:     entry.SourceFile,
			SourceLine:     int64(entry.SourceLine),
			FirstSeenAt:    ts,
			LastSeenAt:     ts,
			LastLogID:      lastLogID,
		})
		if err != nil {
			return fmt.Errorf("insert error group: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("check existing: %w", err)
	} else {
		// Existing error group — update counts.
		err = qtx.IncrementErrorGroupCount(ctx, IncrementErrorGroupCountParams{
			LastSeenAt:  ts,
			LastLogID:   lastLogID,
			Fingerprint: entry.ErrorFingerprint,
		})
		if err != nil {
			return fmt.Errorf("update error group: %w", err)
		}

		// Reopen if it was resolved (not ignored — ignored stays ignored).
		if existingStatus == string(store.ErrorGroupResolved) {
			err = qtx.ReopenErrorGroup(ctx, entry.ErrorFingerprint)
			if err != nil {
				return fmt.Errorf("reopen error group: %w", err)
			}
			err = qtx.InsertErrorGroupEvent(ctx, InsertErrorGroupEventParams{
				Fingerprint: entry.ErrorFingerprint,
				Action:      "reopened",
				Reason:      "New occurrence after resolution",
				CreatedAt:   now,
			})
			if err != nil {
				return fmt.Errorf("insert reopen event: %w", err)
			}
		}
	}

	return tx.Commit()
}

func (s *errorGroupStore) Get(ctx context.Context, fingerprint string) (*store.ErrorGroup, error) {
	row, err := s.q.GetErrorGroup(ctx, fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return errorGroupToStore(row), nil
}

func (s *errorGroupStore) List(ctx context.Context, params store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	qb := psql.Select(
		"fingerprint", "service", "environment", "exception_class", "message",
		"source_file", "source_line", "status", "first_seen_at", "last_seen_at",
		"occurrence_count", "last_log_id", "reopened_count", "resolved_at", "ignored_at",
		"unique_users", "impact_score", "COALESCE(common_context, '{}')").
		From("error_groups")

	if params.Status != "" {
		qb = qb.Where(sq.Eq{"status": string(params.Status)})
	}
	if params.Service != "" {
		qb = qb.Where(sq.Eq{"service": params.Service})
	}
	if params.Environment != "" {
		qb = qb.Where(sq.Eq{"environment": params.Environment})
	}

	orderBy := "last_seen_at DESC"
	switch params.SortBy {
	case "occurrence_count":
		orderBy = "occurrence_count DESC"
	case "first_seen_at":
		orderBy = "first_seen_at DESC"
	}
	qb = qb.OrderBy(orderBy)

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	qb = qb.Limit(uint64(limit)).Offset(uint64(params.Offset))

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building list query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list error groups: %w", err)
	}
	defer rows.Close()

	var groups []store.ErrorGroup
	for rows.Next() {
		eg, err := scanErrorGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, *eg)
	}
	return groups, rows.Err()
}

func (s *errorGroupStore) Count(ctx context.Context, status store.ErrorGroupStatus) (int, error) {
	if status != "" {
		n, err := s.q.CountErrorGroupsByStatus(ctx, string(status))
		return int(n), err
	}
	n, err := s.q.CountAllErrorGroups(ctx)
	return int(n), err
}

func (s *errorGroupStore) Resolve(ctx context.Context, fingerprint string, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qtx := s.q.WithTx(tx)

	n, err := qtx.ResolveErrorGroup(ctx, ResolveErrorGroupParams{
		ResolvedAt:  sql.NullString{String: now, Valid: true},
		Fingerprint: fingerprint,
	})
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}

	err = qtx.InsertErrorGroupEvent(ctx, InsertErrorGroupEventParams{
		Fingerprint: fingerprint,
		Action:      "resolved",
		Reason:      reason,
		CreatedAt:   now,
	})
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

	qtx := s.q.WithTx(tx)

	n, err := qtx.IgnoreErrorGroup(ctx, IgnoreErrorGroupParams{
		IgnoredAt:   sql.NullString{String: now, Valid: true},
		Fingerprint: fingerprint,
	})
	if err != nil {
		return fmt.Errorf("ignore: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}

	err = qtx.InsertErrorGroupEvent(ctx, InsertErrorGroupEventParams{
		Fingerprint: fingerprint,
		Action:      "ignored",
		Reason:      reason,
		CreatedAt:   now,
	})
	if err != nil {
		return fmt.Errorf("insert ignore event: %w", err)
	}

	return tx.Commit()
}

func (s *errorGroupStore) ListEvents(ctx context.Context, fingerprint string, limit int) ([]store.ErrorGroupEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.ListErrorGroupEvents(ctx, ListErrorGroupEventsParams{
		Fingerprint: fingerprint,
		RowLimit:    int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return errorGroupEventsToStore(rows), nil
}

func (s *errorGroupStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UTC().Format(time.RFC3339)
	return s.q.PruneErrorGroups(ctx, cutoff)
}

// scanErrorGroup scans a row into an ErrorGroup.
func scanErrorGroup(sc interface{ Scan(...any) error }) (*store.ErrorGroup, error) {
	var eg store.ErrorGroup
	var firstSeen, lastSeen string
	var resolvedAt, ignoredAt sql.NullString
	var lastLogID sql.NullInt64
	var commonCtxJSON string

	err := sc.Scan(
		&eg.Fingerprint, &eg.Service, &eg.Environment, &eg.ExceptionClass, &eg.Message,
		&eg.SourceFile, &eg.SourceLine, &eg.Status, &firstSeen, &lastSeen,
		&eg.OccurrenceCount, &lastLogID, &eg.ReopenedCount, &resolvedAt, &ignoredAt,
		&eg.UniqueUsers, &eg.ImpactScore, &commonCtxJSON,
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
	if commonCtxJSON != "" && commonCtxJSON != "{}" {
		json.Unmarshal([]byte(commonCtxJSON), &eg.CommonContext)
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
