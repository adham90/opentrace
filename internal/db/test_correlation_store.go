package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type testCorrelationStore struct {
	db *sql.DB
	q  *Queries
}

// NewTestCorrelationStore creates a new TestCorrelationStore backed by SQLite.
func NewTestCorrelationStore(db *sql.DB) store.TestCorrelationStore {
	return &testCorrelationStore{db: db, q: New(db)}
}

func (s *testCorrelationStore) RefreshUncoveredPaths(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.q.RefreshUncoveredPaths(ctx, now)
}

func (s *testCorrelationStore) TopByPriority(ctx context.Context, service string, limit int) ([]store.UncoveredErrorPath, error) {
	if limit <= 0 {
		limit = 10
	}

	if service != "" {
		rows, err := s.q.ListTopByPriorityForService(ctx, ListTopByPriorityForServiceParams{
			Service:    service,
			MaxResults: int64(limit),
		})
		if err != nil {
			return nil, fmt.Errorf("querying uncovered paths: %w", err)
		}
		return toStoreUncoveredPaths(rows), nil
	}

	rows, err := s.q.ListTopByPriority(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("querying uncovered paths: %w", err)
	}
	return toStoreUncoveredPaths(rows), nil
}

func (s *testCorrelationStore) GetByFingerprint(ctx context.Context, fingerprint string) (*store.UncoveredErrorPath, error) {
	row, err := s.q.GetByFingerprint(ctx, fingerprint)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting uncovered path: %w", err)
	}
	p := toStoreUncoveredPath(row)
	return &p, nil
}

func (s *testCorrelationStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		n, err := s.q.PruneTestCorrelations(ctx, cutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning uncovered paths: %w", err)
		}
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}
