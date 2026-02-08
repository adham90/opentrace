package web

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
)

type mockDataSourceStore struct {
	mu      sync.Mutex
	sources map[uuid.UUID]*store.DataSource
}

func newMockStore() *mockDataSourceStore {
	return &mockDataSourceStore{
		sources: make(map[uuid.UUID]*store.DataSource),
	}
}

func (m *mockDataSourceStore) Create(ctx context.Context, params store.CreateDataSourceParams) (*store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ds := &store.DataSource{
		ID:     uuid.New(),
		Type:   params.Type,
		Name:   params.Name,
		Config: params.Config,
		Status: store.StatusDisconnected,
	}
	m.sources[ds.ID] = ds
	return ds, nil
}

func (m *mockDataSourceStore) GetByID(ctx context.Context, id uuid.UUID) (*store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ds, ok := m.sources[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return ds, nil
}

func (m *mockDataSourceStore) List(ctx context.Context) ([]store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]store.DataSource, 0, len(m.sources))
	for _, ds := range m.sources {
		result = append(result, *ds)
	}
	return result, nil
}

func (m *mockDataSourceStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateDataSourceParams) (*store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ds, ok := m.sources[id]
	if !ok {
		return nil, store.ErrNotFound
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

func (m *mockDataSourceStore) Delete(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sources[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.sources, id)
	return nil
}
