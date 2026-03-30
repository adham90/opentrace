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

var _ store.WatchStore = (*WatchStore)(nil)

// ===========================================================================
// WatchStore
// ===========================================================================

// WatchStore is a stub implementing store.WatchStore.
type WatchStore struct {
	mu      sync.Mutex
	Watches map[string]*store.Watch
}

// NewWatchStore returns an initialised WatchStore stub.
func NewWatchStore() *WatchStore {
	return &WatchStore{Watches: make(map[string]*store.Watch)}
}

func (m *WatchStore) Create(_ context.Context, _ store.CreateWatchParams) (*store.Watch, error) {
	return &store.Watch{ID: "stub-watch"}, nil
}
func (m *WatchStore) GetByID(_ context.Context, id string) (*store.Watch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.Watches[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return w, nil
}
func (m *WatchStore) List(_ context.Context, _ store.ListWatchParams) ([]store.Watch, error) {
	return nil, nil
}
func (m *WatchStore) UpdateStatus(_ context.Context, _ string, _ store.WatchStatus) error {
	return nil
}
func (m *WatchStore) UpdateAfterCheck(_ context.Context, _ string, _ float64, _ int, _ time.Time) error {
	return nil
}
func (m *WatchStore) UpdateBaseline(_ context.Context, _ string, _ *store.WatchBaseline) error {
	return nil
}
func (m *WatchStore) Delete(_ context.Context, _ string) error { return nil }
func (m *WatchStore) GetDueWatches(_ context.Context) ([]store.Watch, error) {
	return nil, nil
}
func (m *WatchStore) ExpireWatches(_ context.Context) (int, error) { return 0, nil }
func (m *WatchStore) CreateRun(_ context.Context, _ string) (*store.WatchRun, error) {
	return &store.WatchRun{ID: "stub-run"}, nil
}
func (m *WatchStore) CompleteRun(_ context.Context, _ string, _ float64, _ bool, _ string) error {
	return nil
}
func (m *WatchStore) FailRun(_ context.Context, _ string, _ string) error { return nil }
func (m *WatchStore) ListRuns(_ context.Context, _ string, _ int) ([]store.WatchRun, error) {
	return nil, nil
}
func (m *WatchStore) CreateAlert(_ context.Context, _ store.CreateWatchAlertParams) (*store.WatchAlert, error) {
	return &store.WatchAlert{ID: "stub-alert"}, nil
}
func (m *WatchStore) GetAlert(_ context.Context, _ string) (*store.WatchAlert, error) {
	return nil, store.ErrNotFound
}
func (m *WatchStore) ListAlerts(_ context.Context, _ string, _ string, _ int) ([]store.WatchAlert, error) {
	return nil, nil
}
func (m *WatchStore) DismissAlert(_ context.Context, _ string, _ string) error     { return nil }
func (m *WatchStore) AcknowledgeAlert(_ context.Context, _ string) error           { return nil }
func (m *WatchStore) CountPendingAlerts(_ context.Context) (int, error)            { return 0, nil }
