package mocks

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.ServerStore = (*ServerStore)(nil)
var _ store.MetricStore = (*MetricStore)(nil)

// ===========================================================================
// ServerStore
// ===========================================================================

// ServerStore is a thread-safe in-memory mock implementing store.ServerStore.
type ServerStore struct {
	mu      sync.Mutex
	Servers map[uuid.UUID]*store.Server
	byHost  map[string]uuid.UUID
}

// NewServerStore returns an initialised ServerStore mock.
func NewServerStore() *ServerStore {
	return &ServerStore{
		Servers: make(map[uuid.UUID]*store.Server),
		byHost:  make(map[string]uuid.UUID),
	}
}

func (m *ServerStore) Register(_ context.Context, params store.RegisterServerParams) (*store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, exists := m.byHost[params.Hostname]; exists {
		s := m.Servers[id]
		s.IPAddress = params.IPAddress
		s.OS = params.OS
		s.Arch = params.Arch
		s.AgentVersion = params.AgentVersion
		s.Status = store.ServerOnline
		now := time.Now()
		s.LastSeenAt = &now
		s.UpdatedAt = now
		return s, nil
	}
	now := time.Now()
	s := &store.Server{
		ID:           uuid.New(),
		Hostname:     params.Hostname,
		IPAddress:    params.IPAddress,
		OS:           params.OS,
		Arch:         params.Arch,
		AgentVersion: params.AgentVersion,
		Labels:       params.Labels,
		Status:       store.ServerOnline,
		LastSeenAt:   &now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.Servers[s.ID] = s
	m.byHost[params.Hostname] = s.ID
	return s, nil
}

func (m *ServerStore) GetByID(_ context.Context, id uuid.UUID) (*store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.Servers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return s, nil
}

func (m *ServerStore) List(_ context.Context, params store.ListServerParams) ([]store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Server, 0, len(m.Servers))
	for _, s := range m.Servers {
		result = append(result, *s)
	}
	if params.Limit > 0 {
		start := params.Offset
		if start > len(result) {
			start = len(result)
		}
		end := start + params.Limit
		if end > len(result) {
			end = len(result)
		}
		result = result[start:end]
	}
	return result, nil
}

func (m *ServerStore) Update(_ context.Context, id uuid.UUID, params store.UpdateServerParams) (*store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.Servers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if params.DisplayName != nil {
		s.DisplayName = *params.DisplayName
	}
	s.UpdatedAt = time.Now()
	return s, nil
}

func (m *ServerStore) UpdateHeartbeat(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.Servers[id]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now()
	s.LastSeenAt = &now
	s.Status = store.ServerOnline
	return nil
}

func (m *ServerStore) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.Servers[id]
	if !ok {
		return store.ErrNotFound
	}
	delete(m.byHost, s.Hostname)
	delete(m.Servers, id)
	return nil
}

func (m *ServerStore) MarkStaleOffline(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}

// ===========================================================================
// MetricStore
// ===========================================================================

// MetricStore is a thread-safe in-memory mock implementing store.MetricStore.
type MetricStore struct {
	mu      sync.Mutex
	Metrics []store.MetricPoint
}

// NewMetricStore returns an initialised MetricStore mock.
func NewMetricStore() *MetricStore {
	return &MetricStore{}
}

func (m *MetricStore) BatchInsert(_ context.Context, serverID uuid.UUID, ts time.Time, samples []store.MetricSample) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range samples {
		m.Metrics = append(m.Metrics, store.MetricPoint{
			ServerID:    serverID,
			Timestamp:   ts,
			MetricName:  s.Name,
			MetricValue: s.Value,
			Unit:        s.Unit,
			Labels:      s.Labels,
		})
	}
	return len(samples), nil
}

func (m *MetricStore) Query(_ context.Context, params store.MetricQuery) ([]store.MetricPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []store.MetricPoint
	for _, mp := range m.Metrics {
		if mp.ServerID != params.ServerID {
			continue
		}
		if params.MetricName != "" && mp.MetricName != params.MetricName {
			continue
		}
		result = append(result, mp)
	}
	return result, nil
}

func (m *MetricStore) LatestByServer(_ context.Context, serverID uuid.UUID) ([]store.MetricPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	latest := make(map[string]store.MetricPoint)
	for _, mp := range m.Metrics {
		if mp.ServerID != serverID {
			continue
		}
		if existing, ok := latest[mp.MetricName]; !ok || mp.Timestamp.After(existing.Timestamp) {
			latest[mp.MetricName] = mp
		}
	}
	result := make([]store.MetricPoint, 0, len(latest))
	for _, mp := range latest {
		result = append(result, mp)
	}
	return result, nil
}

func (m *MetricStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
