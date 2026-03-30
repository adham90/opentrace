package mocks

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.QueryMemoryStore = (*QueryMemoryStore)(nil)
var _ store.RunbookEffectivenessStore = (*RunbookEffectivenessStore)(nil)

// ===========================================================================
// QueryMemoryStore
// ===========================================================================

// QueryMemoryStore is a stub implementing store.QueryMemoryStore.
type QueryMemoryStore struct{}

// NewQueryMemoryStore returns an initialised QueryMemoryStore stub.
func NewQueryMemoryStore() *QueryMemoryStore { return &QueryMemoryStore{} }

func (m *QueryMemoryStore) Get(_ context.Context, _ string) (*store.QueryMemory, error) {
	return nil, store.ErrNotFound
}
func (m *QueryMemoryStore) Upsert(_ context.Context, _ store.UpsertQueryMemoryParams) error {
	return nil
}
func (m *QueryMemoryStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// ===========================================================================
// RunbookEffectivenessStore
// ===========================================================================

// RunbookEffectivenessStore is a stub implementing store.RunbookEffectivenessStore.
type RunbookEffectivenessStore struct{}

// NewRunbookEffectivenessStore returns an initialised RunbookEffectivenessStore stub.
func NewRunbookEffectivenessStore() *RunbookEffectivenessStore {
	return &RunbookEffectivenessStore{}
}

func (m *RunbookEffectivenessStore) RecordExecution(_ context.Context, _ string) error { return nil }
func (m *RunbookEffectivenessStore) UpdateOutcome(_ context.Context, _ store.UpdateRunbookEffectivenessParams) error {
	return nil
}
func (m *RunbookEffectivenessStore) GetMostEffective(_ context.Context) (*store.RunbookEffectiveness, error) {
	return nil, nil
}
func (m *RunbookEffectivenessStore) List(_ context.Context) ([]store.RunbookEffectiveness, error) {
	return nil, nil
}
