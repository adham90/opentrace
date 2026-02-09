package web

import (
	"context"
	"encoding/json"
	"sync"
	"time"

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

// mockEmbeddingStore implements store.EmbeddingStore for testing.
type mockEmbeddingStore struct {
	mu     sync.Mutex
	chunks []store.CodeChunk
	err    error
}

func newMockEmbeddingStore() *mockEmbeddingStore {
	return &mockEmbeddingStore{}
}

func (m *mockEmbeddingStore) UpsertChunks(ctx context.Context, chunks []store.CodeChunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.chunks = append(m.chunks, chunks...)
	return nil
}

func (m *mockEmbeddingStore) Search(ctx context.Context, embedding []float64, limit int) ([]store.CodeSearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return nil, nil
}

func (m *mockEmbeddingStore) DeleteByPath(ctx context.Context, filePath string) error {
	return m.err
}

func (m *mockEmbeddingStore) DeleteAll(ctx context.Context) error {
	return m.err
}

// mockChatStore implements store.ChatStore for testing.
type mockChatStore struct {
	mu       sync.Mutex
	chats    map[uuid.UUID]*store.Chat
	messages map[uuid.UUID][]store.Message
}

func newMockChatStore() *mockChatStore {
	return &mockChatStore{
		chats:    make(map[uuid.UUID]*store.Chat),
		messages: make(map[uuid.UUID][]store.Message),
	}
}

func (m *mockChatStore) CreateChat(ctx context.Context, title string) (*store.Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := &store.Chat{ID: uuid.New(), Title: title}
	m.chats[c.ID] = c
	return c, nil
}

func (m *mockChatStore) GetChat(ctx context.Context, id uuid.UUID) (*store.Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.chats[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return c, nil
}

func (m *mockChatStore) ListChats(ctx context.Context) ([]store.Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Chat, 0, len(m.chats))
	for _, c := range m.chats {
		result = append(result, *c)
	}
	return result, nil
}

func (m *mockChatStore) DeleteChat(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.chats[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.chats, id)
	delete(m.messages, id)
	return nil
}

func (m *mockChatStore) UpdateChatTitle(ctx context.Context, id uuid.UUID, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.chats[id]
	if !ok {
		return store.ErrNotFound
	}
	c.Title = title
	return nil
}

func (m *mockChatStore) AddMessage(ctx context.Context, msg store.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.ChatID] = append(m.messages[msg.ChatID], msg)
	return nil
}

func (m *mockChatStore) GetMessages(ctx context.Context, chatID uuid.UUID) ([]store.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messages[chatID], nil
}

// mockMemoryStore implements store.MemoryStore for testing.
type mockMemoryStore struct {
	mu      sync.Mutex
	entries []store.MemoryEntry
}

func newMockMemoryStore() *mockMemoryStore {
	return &mockMemoryStore{}
}

func (m *mockMemoryStore) AddMemory(ctx context.Context, category, content, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, store.MemoryEntry{
		ID:       uuid.New(),
		Category: category,
		Content:  content,
		Source:   source,
	})
	return nil
}

func (m *mockMemoryStore) ListMemories(ctx context.Context, category string) ([]store.MemoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if category == "" {
		return m.entries, nil
	}
	var filtered []store.MemoryEntry
	for _, e := range m.entries {
		if e.Category == category {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

func (m *mockMemoryStore) SearchMemories(ctx context.Context, query string) ([]store.MemoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries, nil
}

func (m *mockMemoryStore) DeleteMemory(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, e := range m.entries {
		if e.ID == id {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
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
	interval := params.IntervalSeconds
	if interval <= 0 {
		interval = 300
	}
	filters := params.Filters
	if filters == nil {
		filters = json.RawMessage(`{}`)
	}
	notify := params.Notify
	if notify == nil {
		notify = json.RawMessage(`["dashboard"]`)
	}
	now := time.Now()
	w := &store.Watcher{
		ID: uuid.New(), Title: params.Title, Description: params.Description,
		Severity: sev, Filters: filters, IntervalSeconds: interval,
		Status: store.WatcherActive, Notify: notify, NextRunAt: &now,
		CreatedAt: now, UpdatedAt: now,
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
