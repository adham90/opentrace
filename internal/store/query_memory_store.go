package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type queryMemoryStore struct {
	db *sql.DB
}

// NewQueryMemoryStore creates a new QueryMemoryStore backed by SQLite.
func NewQueryMemoryStore(db *sql.DB) QueryMemoryStore {
	return &queryMemoryStore{db: db}
}

func (s *queryMemoryStore) Get(ctx context.Context, fingerprint string) (*QueryMemory, error) {
	var qm QueryMemory
	var firstSeen, lastSeen string
	var avgBefore, avgAfter sql.NullInt64

	err := s.db.QueryRowContext(ctx,
		`SELECT fingerprint, last_investigation_session_id, investigation_count,
		        last_root_cause, last_fix, avg_duration_before_ms, avg_duration_after_ms,
		        first_seen_at, last_seen_at
		 FROM query_memory WHERE fingerprint = ?`, fingerprint,
	).Scan(
		&qm.Fingerprint, &qm.LastInvestigationSessionID, &qm.InvestigationCount,
		&qm.LastRootCause, &qm.LastFix, &avgBefore, &avgAfter,
		&firstSeen, &lastSeen,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting query memory: %w", err)
	}

	if avgBefore.Valid {
		v := int(avgBefore.Int64)
		qm.AvgDurationBeforeMs = &v
	}
	if avgAfter.Valid {
		v := int(avgAfter.Int64)
		qm.AvgDurationAfterMs = &v
	}
	qm.FirstSeenAt, _ = time.Parse(time.RFC3339, firstSeen)
	qm.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeen)

	return &qm, nil
}

func (s *queryMemoryStore) Upsert(ctx context.Context, params UpsertQueryMemoryParams) error {
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
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM query_memory WHERE last_seen_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning query memory: %w", err)
	}
	return result.RowsAffected()
}
