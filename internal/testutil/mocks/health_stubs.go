package mocks

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface check
// ---------------------------------------------------------------------------

var _ store.HealthCheckStore = (*HealthCheckStore)(nil)

// ===========================================================================
// HealthCheckStore
// ===========================================================================

// HealthCheckStore is a stub implementing store.HealthCheckStore.
type HealthCheckStore struct{}

// NewHealthCheckStore returns an initialised HealthCheckStore stub.
func NewHealthCheckStore() *HealthCheckStore { return &HealthCheckStore{} }

func (m *HealthCheckStore) Create(_ context.Context, _ store.CreateHealthCheckParams) (*store.HealthCheck, error) {
	return &store.HealthCheck{ID: "stub-hc"}, nil
}
func (m *HealthCheckStore) Get(_ context.Context, _ string) (*store.HealthCheck, error) {
	return nil, store.ErrNotFound
}
func (m *HealthCheckStore) List(_ context.Context, _ store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	return nil, nil
}
func (m *HealthCheckStore) Delete(_ context.Context, _ string) error              { return nil }
func (m *HealthCheckStore) SetEnabled(_ context.Context, _ string, _ bool) error  { return nil }
func (m *HealthCheckStore) RecordResult(_ context.Context, _ store.HealthCheckResult) error {
	return nil
}
func (m *HealthCheckStore) LatestResults(_ context.Context, _ string, _ int) ([]store.HealthCheckResult, error) {
	return nil, nil
}
func (m *HealthCheckStore) UptimeSummaries(_ context.Context, _ time.Time) ([]store.UptimeSummary, error) {
	return nil, nil
}
func (m *HealthCheckStore) PruneResults(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
