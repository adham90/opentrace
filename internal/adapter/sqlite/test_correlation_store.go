package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type testCorrelationStore struct {
	db *bun.DB
}

// NewTestCorrelationStore creates a new TestCorrelationStore backed by SQLite.
func NewTestCorrelationStore(db *bun.DB) store.TestCorrelationStore {
	return &testCorrelationStore{db: db}
}

func (s *testCorrelationStore) RefreshUncoveredPaths(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.NewRaw(`
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
		  AND eg.occurrence_count >= 3`,
		now, now,
	).Exec(ctx)
	return err
}

func (s *testCorrelationStore) TopByPriority(ctx context.Context, service string, limit int) ([]store.UncoveredErrorPath, error) {
	if limit <= 0 {
		limit = 10
	}

	var paths []store.UncoveredErrorPath
	q := s.db.NewSelect().Model(&paths)
	if service != "" {
		q = q.Where("service = ?", service)
	}
	q = q.OrderExpr("priority_score DESC").Limit(limit)
	err := q.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying uncovered paths: %w", err)
	}
	return paths, nil
}

func (s *testCorrelationStore) GetByFingerprint(ctx context.Context, fingerprint string) (*store.UncoveredErrorPath, error) {
	p := new(store.UncoveredErrorPath)
	err := s.db.NewSelect().Model(p).
		Where("error_fingerprint = ?", fingerprint).
		Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting uncovered path: %w", err)
	}
	return p, nil
}

func (s *testCorrelationStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		res, err := s.db.NewRaw(
			"DELETE FROM uncovered_error_paths WHERE rowid IN (SELECT u.rowid FROM uncovered_error_paths u WHERE u.updated_at < ? LIMIT 1000)",
			cutoff,
		).Exec(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning uncovered paths: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}
