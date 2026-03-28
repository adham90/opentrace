package mocks

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/pkg/store"
)

// Compile-time interface check.
var _ store.DataSourceStore = (*DataSourceStore)(nil)

// DataSourceStore is a thread-safe in-memory mock implementing store.DataSourceStore.
type DataSourceStore struct {
	mu      sync.Mutex
	Sources map[uuid.UUID]*store.DataSource
}

// NewDataSourceStore returns an initialised DataSourceStore mock.
func NewDataSourceStore() *DataSourceStore {
	return &DataSourceStore{
		Sources: make(map[uuid.UUID]*store.DataSource),
	}
}

func (m *DataSourceStore) Create(_ context.Context, params store.CreateDataSourceParams) (*store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ds := &store.DataSource{
		ID:     uuid.New(),
		Type:   params.Type,
		Name:   params.Name,
		Config: params.Config,
		Status: store.StatusDisconnected,
	}
	m.Sources[ds.ID] = ds
	return ds, nil
}

func (m *DataSourceStore) GetByID(_ context.Context, id uuid.UUID) (*store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ds, ok := m.Sources[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return ds, nil
}

func (m *DataSourceStore) List(_ context.Context, _ store.ListDataSourceParams) ([]store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.DataSource, 0, len(m.Sources))
	for _, ds := range m.Sources {
		result = append(result, *ds)
	}
	return result, nil
}

func (m *DataSourceStore) Update(_ context.Context, id uuid.UUID, params store.UpdateDataSourceParams) (*store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ds, ok := m.Sources[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if params.Name != nil {
		ds.Name = *params.Name
	}
	if params.Config != nil {
		ds.Config = params.Config
	}
	if params.Status != nil {
		ds.Status = *params.Status
	}
	if params.StatusMessage != nil {
		ds.StatusMessage = params.StatusMessage
	}
	if params.LastTestedAt != nil {
		ds.LastTestedAt = params.LastTestedAt
	}
	return ds, nil
}

func (m *DataSourceStore) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Sources[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.Sources, id)
	return nil
}
