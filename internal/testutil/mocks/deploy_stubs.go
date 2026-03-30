package mocks

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.DeployStore = (*DeployStore)(nil)
var _ store.EventStore = (*EventStore)(nil)

// ===========================================================================
// DeployStore
// ===========================================================================

// DeployStore is a stub implementing store.DeployStore.
type DeployStore struct{}

// NewDeployStore returns an initialised DeployStore stub.
func NewDeployStore() *DeployStore { return &DeployStore{} }

func (m *DeployStore) Create(_ context.Context, _ store.CreateDeployParams) (*store.Deploy, error) {
	return &store.Deploy{ID: 1, Status: store.DeployStatusPending}, nil
}
func (m *DeployStore) GetByID(_ context.Context, _ int64) (*store.Deploy, error) {
	return nil, store.ErrNotFound
}
func (m *DeployStore) GetByCommit(_ context.Context, _ string) (*store.Deploy, error) {
	return nil, store.ErrNotFound
}
func (m *DeployStore) GetRecent(_ context.Context, _ string, _ int) ([]store.Deploy, error) {
	return nil, nil
}
func (m *DeployStore) MeasureImpact(_ context.Context, _ int64, _ store.DeployImpact) error {
	return nil
}
func (m *DeployStore) LinkInvestigation(_ context.Context, _ int64, _ string) error { return nil }
func (m *DeployStore) GetPendingMeasurement(_ context.Context, _ time.Duration) ([]store.Deploy, error) {
	return nil, nil
}
func (m *DeployStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// EventStore
// ===========================================================================

// EventStore is a stub implementing store.EventStore.
type EventStore struct{}

// NewEventStore returns an initialised EventStore stub.
func NewEventStore() *EventStore { return &EventStore{} }

func (m *EventStore) Create(_ context.Context, p store.CreateEventParams) (*store.Event, error) {
	return &store.Event{ID: 1, EventType: p.EventType, Title: p.Title}, nil
}
func (m *EventStore) GetByID(_ context.Context, _ int64) (*store.Event, error) {
	return nil, store.ErrNotFound
}
func (m *EventStore) List(_ context.Context, _ store.ListEventParams) ([]store.Event, error) {
	return nil, nil
}
func (m *EventStore) GetByExternalID(_ context.Context, _ store.EventType, _ string) (*store.Event, error) {
	return nil, store.ErrNotFound
}
func (m *EventStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
