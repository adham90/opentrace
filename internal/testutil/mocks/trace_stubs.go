package mocks

import (
	"context"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface check
// ---------------------------------------------------------------------------

var _ store.TraceStore = (*TraceStore)(nil)

// ===========================================================================
// TraceStore
// ===========================================================================

// TraceStore is a stub implementing store.TraceStore.
type TraceStore struct {
	mu     sync.Mutex
	Traces map[string]*store.TraceStatus
}

// NewTraceStore returns an initialised TraceStore stub.
func NewTraceStore() *TraceStore {
	return &TraceStore{Traces: make(map[string]*store.TraceStatus)}
}

func (m *TraceStore) UpsertTraceStatus(_ context.Context, traceID string, entry store.LogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if traceID == "" {
		return nil
	}
	ts, ok := m.Traces[traceID]
	if !ok {
		ts = &store.TraceStatus{
			TraceID:       traceID,
			SpanCount:     0,
			Services:      []string{},
			FirstSeenAt:   entry.Timestamp,
			LastUpdatedAt: time.Now(),
			Status:        "partial",
		}
		m.Traces[traceID] = ts
	}
	ts.SpanCount++
	ts.LastUpdatedAt = time.Now()
	return nil
}

func (m *TraceStore) GetTraceStatus(_ context.Context, traceID string) (*store.TraceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts, ok := m.Traces[traceID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return ts, nil
}

func (m *TraceStore) ListRecentTraces(_ context.Context, limit, offset int) ([]store.TraceStatus, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []store.TraceStatus
	for _, ts := range m.Traces {
		result = append(result, *ts)
	}
	total := len(result)
	if offset > len(result) {
		offset = len(result)
	}
	result = result[offset:]
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}
	return result, total, nil
}

func (m *TraceStore) MarkStaleTraces(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}
