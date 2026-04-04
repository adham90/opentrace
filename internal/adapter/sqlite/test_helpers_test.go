package sqlite

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// NewLogStore creates a no-op log store for tests.
// The logs and request_summaries tables no longer exist in SQLite —
// log storage is handled by the segmented log store engine.
// Tests that need to populate analytics/trend tables should insert
// directly into endpoint_stats/metric_buckets/traffic_heatmap.
func NewLogStore(_ *bun.DB) store.LogStore {
	return &noopLogStore{}
}

// noopLogStore satisfies the interface but does nothing.
// Analytics/trend tests need updating to not depend on this.
type noopLogStore struct{}

func (s *noopLogStore) BatchInsert(_ context.Context, entries []store.LogEntry) (int, error) {
	return len(entries), nil
}
func (s *noopLogStore) Search(_ context.Context, _ store.LogSearchParams) ([]store.LogEntry, error) {
	return nil, nil
}
func (s *noopLogStore) Prune(_ context.Context, _ time.Duration) (int64, error)   { return 0, nil }
func (s *noopLogStore) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return nil, nil
}
func (s *noopLogStore) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}
func (s *noopLogStore) Histogram(_ context.Context, _ store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}
func (s *noopLogStore) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (s *noopLogStore) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (s *noopLogStore) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) { return nil, nil }
func (s *noopLogStore) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}
func (s *noopLogStore) AggregateRequestSummaries(_ context.Context, _ store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return nil, nil
}
func (s *noopLogStore) RecordBatch(_ context.Context, _ string, _ int) error       { return nil }
func (s *noopLogStore) GetBatch(_ context.Context, _ string) (*store.BatchRecord, error) {
	return nil, nil
}
func (s *noopLogStore) PruneBatches(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
