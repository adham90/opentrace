package mocks

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.CodeEntityStore = (*CodeEntityStore)(nil)

// ===========================================================================
// CodeEntityStore
// ===========================================================================

// CodeEntityStore is a stub implementing store.CodeEntityStore.
type CodeEntityStore struct{}

// NewCodeEntityStore returns an initialised CodeEntityStore stub.
func NewCodeEntityStore() *CodeEntityStore { return &CodeEntityStore{} }

func (m *CodeEntityStore) Upsert(_ context.Context, _ store.UpsertCodeEntityParams) (*store.CodeEntity, error) {
	return &store.CodeEntity{}, nil
}
func (m *CodeEntityStore) GetByName(_ context.Context, _ store.CodeEntityType, _, _ string) (*store.CodeEntity, error) {
	return nil, store.ErrNotFound
}
func (m *CodeEntityStore) TopByRisk(_ context.Context, _ string, _ int) ([]store.CodeEntity, error) {
	return nil, nil
}
func (m *CodeEntityStore) BatchGetRisk(_ context.Context, _ store.CodeEntityType, _ []string, _ string) ([]store.CodeEntity, error) {
	return nil, nil
}
func (m *CodeEntityStore) IncrementError(_ context.Context, _ store.CodeEntityType, _, _ string) error {
	return nil
}
func (m *CodeEntityStore) IncrementInvestigation(_ context.Context, _ store.CodeEntityType, _, _ string) error {
	return nil
}
func (m *CodeEntityStore) BatchRecomputeRisk(_ context.Context) error { return nil }
func (m *CodeEntityStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
