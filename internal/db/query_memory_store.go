package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type queryMemoryStore struct {
	db *sql.DB
	q  *Queries
}

// NewQueryMemoryStore creates a new QueryMemoryStore backed by SQLite.
func NewQueryMemoryStore(db *sql.DB) store.QueryMemoryStore {
	return &queryMemoryStore{db: db, q: New(db)}
}

func (s *queryMemoryStore) Get(ctx context.Context, fingerprint string) (*store.QueryMemory, error) {
	row, err := s.q.GetQueryMemory(ctx, fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting query memory: %w", err)
	}
	return toStoreQueryMemory(row), nil
}

// Upsert uses hand-written SQL because the sqlc-generated query has unexpanded
// sqlc.arg() references in the ON CONFLICT clause.
func (s *queryMemoryStore) Upsert(ctx context.Context, params store.UpsertQueryMemoryParams) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
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
	)
	if err != nil {
		return fmt.Errorf("upserting query memory: %w", err)
	}
	return nil
}

func (s *queryMemoryStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	n, err := s.q.PruneQueryMemory(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning query memory: %w", err)
	}
	return n, nil
}
