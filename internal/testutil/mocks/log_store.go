package mocks

import (
	"context"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Compile-time interface check.
var _ store.LogStore = (*LogStore)(nil)

// LogStore is a thread-safe in-memory mock implementing store.LogStore.
type LogStore struct {
	mu               sync.Mutex
	Entries          []store.LogEntry
	Err              error
	Batches          map[string]*store.BatchRecord
	LastSearchParams store.LogSearchParams
}

// NewLogStore returns an initialised LogStore mock.
func NewLogStore() *LogStore {
	return &LogStore{
		Batches: make(map[string]*store.BatchRecord),
	}
}

func (m *LogStore) BatchInsert(_ context.Context, entries []store.LogEntry) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return 0, m.Err
	}
	m.Entries = append(m.Entries, entries...)
	return len(entries), nil
}

func (m *LogStore) Search(_ context.Context, params store.LogSearchParams) ([]store.LogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastSearchParams = params
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Entries, nil
}

func (m *LogStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *LogStore) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return nil, nil
}

func (m *LogStore) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}

func (m *LogStore) Histogram(_ context.Context, _ store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}

func (m *LogStore) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *LogStore) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *LogStore) GetByID(_ context.Context, id int64) (*store.LogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Entries {
		if m.Entries[i].ID == id {
			return &m.Entries[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *LogStore) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}

func (m *LogStore) AggregateRequestSummaries(_ context.Context, _ store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return &store.RequestSummaryAggregates{}, nil
}

func (m *LogStore) RecordBatch(_ context.Context, batchID string, logCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Batches[batchID] = &store.BatchRecord{BatchID: batchID, LogCount: logCount, ReceivedAt: time.Now()}
	return nil
}

func (m *LogStore) GetBatch(_ context.Context, batchID string) (*store.BatchRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.Batches[batchID]
	if !ok {
		return nil, nil
	}
	return rec, nil
}

func (m *LogStore) PruneBatches(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
