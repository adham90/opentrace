package mocks

import (
	"context"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.ErrorGroupStore = (*ErrorGroupStore)(nil)
var _ store.ErrorImpactStore = (*ErrorImpactStore)(nil)

// ===========================================================================
// ErrorGroupStore
// ===========================================================================

// ErrorGroupStore is a stub implementing store.ErrorGroupStore.
type ErrorGroupStore struct {
	mu     sync.Mutex
	Groups map[string]*store.ErrorGroup
}

// NewErrorGroupStore returns an initialised ErrorGroupStore stub.
func NewErrorGroupStore() *ErrorGroupStore {
	return &ErrorGroupStore{Groups: make(map[string]*store.ErrorGroup)}
}

func (m *ErrorGroupStore) Upsert(_ context.Context, _ store.LogEntry) error { return nil }
func (m *ErrorGroupStore) Get(_ context.Context, fingerprint string) (*store.ErrorGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.Groups[fingerprint]
	if !ok {
		return nil, store.ErrNotFound
	}
	return g, nil
}
func (m *ErrorGroupStore) List(_ context.Context, _ store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	return nil, nil
}
func (m *ErrorGroupStore) Count(_ context.Context, _ store.ErrorGroupStatus) (int, error) {
	return 0, nil
}
func (m *ErrorGroupStore) Resolve(_ context.Context, _ string, _ string) error { return nil }
func (m *ErrorGroupStore) Ignore(_ context.Context, _ string, _ string) error  { return nil }
func (m *ErrorGroupStore) Reopen(_ context.Context, _ string, _ string) error  { return nil }
func (m *ErrorGroupStore) ListEvents(_ context.Context, _ string, _ int) ([]store.ErrorGroupEvent, error) {
	return nil, nil
}
func (m *ErrorGroupStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// ErrorImpactStore
// ===========================================================================

// ErrorImpactStore is a stub implementing store.ErrorImpactStore.
type ErrorImpactStore struct{}

// NewErrorImpactStore returns an initialised ErrorImpactStore stub.
func NewErrorImpactStore() *ErrorImpactStore { return &ErrorImpactStore{} }

func (m *ErrorImpactStore) TrackImpact(_ context.Context, _ string, _ string, _ string, _ map[string]any, _ int64, _ string) error {
	return nil
}
func (m *ErrorImpactStore) GetImpact(_ context.Context, _ string) (*store.ErrorImpact, error) {
	return nil, store.ErrNotFound
}
func (m *ErrorImpactStore) GetAffectedUsers(_ context.Context, _ string, _ int) ([]store.AffectedUser, error) {
	return nil, nil
}
func (m *ErrorImpactStore) GetUserErrors(_ context.Context, _ string, _ time.Time) ([]store.ErrorSummary, error) {
	return nil, nil
}
func (m *ErrorImpactStore) ComputeImpactScores(_ context.Context) error { return nil }
func (m *ErrorImpactStore) TopByImpact(_ context.Context, _ store.ImpactQueryParams) ([]store.ErrorGroupWithImpact, error) {
	return nil, nil
}
func (m *ErrorImpactStore) FindCommonTraits(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}
