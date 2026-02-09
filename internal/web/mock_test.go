package web

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
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

// mockLogStore implements store.LogStore for testing.
type mockLogStore struct {
	mu           sync.Mutex
	entries      []store.LogEntry
	err          error
	lastSearchParams store.LogSearchParams
}

func newMockLogStore() *mockLogStore {
	return &mockLogStore{}
}

func (m *mockLogStore) BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return 0, m.err
	}
	m.entries = append(m.entries, entries...)
	return len(entries), nil
}

func (m *mockLogStore) Search(ctx context.Context, params store.LogSearchParams) ([]store.LogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastSearchParams = params
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

// mockWatcherStore implements store.WatcherStore for testing.
type mockWatcherStore struct {
	mu       sync.Mutex
	watchers map[uuid.UUID]*store.Watcher
}

func newMockWatcherStore() *mockWatcherStore {
	return &mockWatcherStore{watchers: make(map[uuid.UUID]*store.Watcher)}
}

func (m *mockWatcherStore) Create(ctx context.Context, params store.CreateWatcherParams) (*store.Watcher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sev := params.Severity
	if sev == "" {
		sev = store.SeverityWarning
	}
	timeRange := params.TimeRange
	if timeRange == "" {
		timeRange = "15m"
	}
	filters := params.Filters
	if filters == nil {
		filters = json.RawMessage(`{}`)
	}
	notify := params.Notify
	if notify == nil {
		notify = json.RawMessage(`["dashboard"]`)
	}
	effort := params.Effort
	if effort == "" {
		effort = store.EffortMedium
	}
	now := time.Now()
	w := &store.Watcher{
		ID: uuid.New(), Title: params.Title, Description: params.Description,
		Severity: sev, Filters: filters, TimeRange: timeRange,
		Model: params.Model, Effort: effort, Status: store.WatcherActive, Notify: notify,
		NextRunAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	m.watchers[w.ID] = w
	return w, nil
}

func (m *mockWatcherStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Watcher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.watchers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return w, nil
}

func (m *mockWatcherStore) List(ctx context.Context) ([]store.Watcher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Watcher, 0, len(m.watchers))
	for _, w := range m.watchers {
		result = append(result, *w)
	}
	return result, nil
}

func (m *mockWatcherStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateWatcherParams) (*store.Watcher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.watchers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if params.Title != nil {
		w.Title = *params.Title
	}
	if params.Description != nil {
		w.Description = *params.Description
	}
	if params.Severity != nil {
		w.Severity = *params.Severity
	}
	if params.Model != nil {
		w.Model = *params.Model
	}
	if params.Effort != nil {
		w.Effort = *params.Effort
	}
	w.UpdatedAt = time.Now()
	return w, nil
}

func (m *mockWatcherStore) UpdateStatus(ctx context.Context, id uuid.UUID, status store.WatcherStatus) (*store.Watcher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.watchers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	w.Status = status
	w.UpdatedAt = time.Now()
	return w, nil
}

func (m *mockWatcherStore) Delete(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.watchers[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.watchers, id)
	return nil
}

func (m *mockWatcherStore) GetDueWatchers(ctx context.Context) ([]store.Watcher, error) {
	return nil, nil
}

func (m *mockWatcherStore) UpdateRunTime(ctx context.Context, id uuid.UUID, lastRun, nextRun time.Time) error {
	return nil
}

// mockWatcherRunStore implements store.WatcherRunStore for testing.
type mockWatcherRunStore struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*store.WatcherRun
	byW  map[uuid.UUID][]uuid.UUID
}

func newMockWatcherRunStore() *mockWatcherRunStore {
	return &mockWatcherRunStore{
		runs: make(map[uuid.UUID]*store.WatcherRun),
		byW:  make(map[uuid.UUID][]uuid.UUID),
	}
}

func (m *mockWatcherRunStore) Create(ctx context.Context, watcherID uuid.UUID) (*store.WatcherRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := &store.WatcherRun{ID: uuid.New(), WatcherID: watcherID, StartedAt: time.Now(), Status: "running", CreatedAt: time.Now()}
	m.runs[r.ID] = r
	m.byW[watcherID] = append(m.byW[watcherID], r.ID)
	return r, nil
}

func (m *mockWatcherRunStore) Complete(ctx context.Context, id uuid.UUID, summary string, details any, hasAlert bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil {
		return store.ErrNotFound
	}
	now := time.Now()
	r.Status = "completed"
	r.Summary = &summary
	r.HasAlert = hasAlert
	r.FinishedAt = &now
	return nil
}

func (m *mockWatcherRunStore) Fail(ctx context.Context, id uuid.UUID, errMsg string) error {
	return nil
}

func (m *mockWatcherRunStore) List(ctx context.Context, watcherID uuid.UUID, limit int) ([]store.WatcherRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []store.WatcherRun
	for _, id := range m.byW[watcherID] {
		if r := m.runs[id]; r != nil {
			result = append(result, *r)
		}
	}
	return result, nil
}

func (m *mockWatcherRunStore) GetByID(ctx context.Context, id uuid.UUID) (*store.WatcherRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil {
		return nil, store.ErrNotFound
	}
	return r, nil
}

// mockAlertStore implements store.AlertStore for testing.
type mockAlertStore struct {
	mu     sync.Mutex
	alerts map[uuid.UUID]*store.Alert
}

func newMockAlertStore() *mockAlertStore {
	return &mockAlertStore{alerts: make(map[uuid.UUID]*store.Alert)}
}

func (m *mockAlertStore) Create(ctx context.Context, params store.CreateAlertParams) (*store.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sev := params.Severity
	if sev == "" {
		sev = store.SeverityWarning
	}
	a := &store.Alert{
		ID: uuid.New(), WatcherID: params.WatcherID, RunID: params.RunID,
		Title: params.Title, Summary: params.Summary, Severity: sev,
		Details: params.Details, CreatedAt: time.Now(),
	}
	m.alerts[a.ID] = a
	return a, nil
}

func (m *mockAlertStore) List(ctx context.Context, params store.ListAlertParams) ([]store.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Alert, 0, len(m.alerts))
	for _, a := range m.alerts {
		if params.UnreadOnly && (a.Read || a.Dismissed) {
			continue
		}
		result = append(result, *a)
	}
	return result, nil
}

func (m *mockAlertStore) CountUnread(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, a := range m.alerts {
		if !a.Read && !a.Dismissed {
			count++
		}
	}
	return count, nil
}

func (m *mockAlertStore) MarkRead(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.alerts[id]
	if !ok {
		return store.ErrNotFound
	}
	a.Read = true
	return nil
}

func (m *mockAlertStore) Dismiss(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.alerts[id]
	if !ok {
		return store.ErrNotFound
	}
	a.Dismissed = true
	return nil
}
