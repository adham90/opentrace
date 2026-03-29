package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type queryMemoryStore struct {
	db *bun.DB
}

// NewQueryMemoryStore creates a new QueryMemoryStore backed by SQLite.
func NewQueryMemoryStore(db *bun.DB) store.QueryMemoryStore {
	return &queryMemoryStore{db: db}
}

func (s *queryMemoryStore) Get(ctx context.Context, fingerprint string) (*store.QueryMemory, error) {
	var qm store.QueryMemory
	err := s.db.NewSelect().Model(&qm).
		Where("fingerprint = ?", fingerprint).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting query memory: %w", err)
	}
	return &qm, nil
}

// Upsert uses raw SQL because the ON CONFLICT clause has conditional SET logic.
func (s *queryMemoryStore) Upsert(ctx context.Context, params store.UpsertQueryMemoryParams) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.NewRaw(
		`INSERT INTO query_memory (fingerprint, last_investigation_session_id, investigation_count,
		                           last_root_cause, last_fix, avg_duration_before_ms,
		                           first_seen_at, last_seen_at)
		 VALUES (?, ?, 1, ?, ?, ?, ?, ?)
		 ON CONFLICT(fingerprint) DO UPDATE SET
		     investigation_count = investigation_count + 1,
		     last_investigation_session_id = ?,
		     last_root_cause = CASE WHEN ? != '' THEN ? ELSE last_root_cause END,
		     last_fix = CASE WHEN ? != '' THEN ? ELSE last_fix END,
		     last_seen_at = ?`,
		params.Fingerprint, params.SessionID,
		params.RootCause, params.Fix, params.DurationMs,
		now, now,
		// ON CONFLICT args
		params.SessionID,
		params.RootCause, params.RootCause,
		params.Fix, params.Fix,
		now,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("upserting query memory: %w", err)
	}
	return nil
}

func (s *queryMemoryStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.NewDelete().Model((*store.QueryMemory)(nil)).
		Where("last_seen_at < ?", cutoff).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("pruning query memory: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
