package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type testCorrelationStore struct {
	db *sql.DB
}

// NewTestCorrelationStore creates a new TestCorrelationStore backed by SQLite.
func NewTestCorrelationStore(db *sql.DB) store.TestCorrelationStore {
	return &testCorrelationStore{db: db}
}

// RefreshUncoveredPaths rebuilds the uncovered_error_paths table by joining
// error_groups with code_entities and error_impact data. Only active error
// groups with >= 3 occurrences are included.
// Priority = error_count * max(impact_score, 1.0) * (1 + investigation_count).
func (s *testCorrelationStore) RefreshUncoveredPaths(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO uncovered_error_paths
			(error_fingerprint, service, error_class, source_file, endpoint,
			 error_count, user_impact_score, investigation_count, priority_score,
			 last_seen_at, updated_at)
		SELECT
			eg.fingerprint,
			COALESCE(eg.service, ''),
			COALESCE(eg.exception_class, ''),
			COALESCE(eg.source_file, ''),
			COALESCE(ce.entity_name, ''),
			eg.occurrence_count,
			COALESCE(eg.impact_score, 0.0),
			COALESCE(ce.investigation_count, 0),
			eg.occurrence_count * MAX(COALESCE(eg.impact_score, 1.0), 1.0) * (1 + COALESCE(ce.investigation_count, 0)),
			COALESCE(eg.last_seen_at, ?),
			?
		FROM error_groups eg
		LEFT JOIN code_entities ce ON (
			ce.entity_type = 'endpoint'
			AND ce.service = eg.service
		)
		WHERE eg.status = 'unresolved'
		  AND eg.occurrence_count >= 3
	`, now, now)
	if err != nil {
		return fmt.Errorf("refreshing uncovered paths: %w", err)
	}
	return nil
}

func (s *testCorrelationStore) TopByPriority(ctx context.Context, service string, limit int) ([]store.UncoveredErrorPath, error) {
	if limit <= 0 {
		limit = 10
	}

	var query string
	var args []any
	if service != "" {
		query = `SELECT id, service, error_fingerprint, error_class, source_file, endpoint,
				        error_count, user_impact_score, investigation_count, priority_score,
				        last_seen_at, created_at, updated_at
				 FROM uncovered_error_paths
				 WHERE service = ?
				 ORDER BY priority_score DESC
				 LIMIT ?`
		args = []any{service, limit}
	} else {
		query = `SELECT id, service, error_fingerprint, error_class, source_file, endpoint,
				        error_count, user_impact_score, investigation_count, priority_score,
				        last_seen_at, created_at, updated_at
				 FROM uncovered_error_paths
				 ORDER BY priority_score DESC
				 LIMIT ?`
		args = []any{limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying uncovered paths: %w", err)
	}
	defer rows.Close()

	var paths []store.UncoveredErrorPath
	for rows.Next() {
		p, err := scanUncoveredPath(rows)
		if err != nil {
			return nil, err
		}
		paths = append(paths, *p)
	}
	return paths, rows.Err()
}

func (s *testCorrelationStore) GetByFingerprint(ctx context.Context, fingerprint string) (*store.UncoveredErrorPath, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, service, error_fingerprint, error_class, source_file, endpoint,
		        error_count, user_impact_score, investigation_count, priority_score,
		        last_seen_at, created_at, updated_at
		 FROM uncovered_error_paths
		 WHERE error_fingerprint = ?`, fingerprint)

	var p store.UncoveredErrorPath
	var lastSeenAt, createdAt, updatedAt string
	err := row.Scan(
		&p.ID, &p.Service, &p.ErrorFingerprint, &p.ErrorClass,
		&p.SourceFile, &p.Endpoint, &p.ErrorCount, &p.UserImpactScore,
		&p.InvestigationCount, &p.PriorityScore,
		&lastSeenAt, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting uncovered path: %w", err)
	}
	p.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeenAt)
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &p, nil
}

func (s *testCorrelationStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		result, err := s.db.ExecContext(ctx,
			`DELETE FROM uncovered_error_paths
			 WHERE rowid IN (SELECT rowid FROM uncovered_error_paths WHERE updated_at < ? LIMIT 1000)`,
			cutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning uncovered paths: %w", err)
		}
		n, _ := result.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func scanUncoveredPath(rows *sql.Rows) (*store.UncoveredErrorPath, error) {
	var p store.UncoveredErrorPath
	var lastSeenAt, createdAt, updatedAt string
	err := rows.Scan(
		&p.ID, &p.Service, &p.ErrorFingerprint, &p.ErrorClass,
		&p.SourceFile, &p.Endpoint, &p.ErrorCount, &p.UserImpactScore,
		&p.InvestigationCount, &p.PriorityScore,
		&lastSeenAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning uncovered path: %w", err)
	}
	p.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeenAt)
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &p, nil
}
