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
		ID:          uuid.New(),
		Type:        params.Type,
		Name:        params.Name,
		Environment: params.Environment,
		Config:      params.Config,
		Status:      store.StatusDisconnected,
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

func (m *mockDataSourceStore) List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]store.DataSource, 0, len(m.sources))
	for _, ds := range m.sources {
		if params.Environment != "" && ds.Environment != params.Environment {
			continue
		}
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

func (m *mockLogStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
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
	monitorType := params.MonitorType
	if monitorType == "" {
		monitorType = store.MonitorTypeAI
	}
	now := time.Now()
	w := &store.Watcher{
		ID: uuid.New(), Title: params.Title, Description: params.Description,
		Environment: params.Environment,
		Severity: sev, Filters: filters, TimeRange: timeRange,
		Model: params.Model, Effort: effort, Status: store.WatcherActive, Notify: notify,
		MonitorType: monitorType, RuleConfig: params.RuleConfig, DataSourceID: params.DataSourceID,
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

func (m *mockWatcherStore) List(ctx context.Context, params store.ListWatcherParams) ([]store.Watcher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Watcher, 0, len(m.watchers))
	for _, w := range m.watchers {
		if params.Environment != "" && w.Environment != params.Environment {
			continue
		}
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
	if params.Environment != nil {
		w.Environment = *params.Environment
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

func (m *mockWatcherRunStore) FailStaleRuns(ctx context.Context, olderThan time.Duration) (int, error) {
	return 0, nil
}

func (m *mockWatcherRunStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
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
		Title: params.Title, Summary: params.Summary, Environment: params.Environment,
		Severity: sev, Details: params.Details, CreatedAt: time.Now(),
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

func (m *mockAlertStore) CountUnread(ctx context.Context, environment string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, a := range m.alerts {
		if !a.Read && !a.Dismissed {
			if environment != "" && a.Environment != environment {
				continue
			}
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

func (m *mockAlertStore) CountTotal(ctx context.Context, environment string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, a := range m.alerts {
		if !a.Dismissed {
			if environment != "" && a.Environment != environment {
				continue
			}
			count++
		}
	}
	return count, nil
}

func (m *mockAlertStore) MarkAllRead(ctx context.Context, environment string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.alerts {
		if !a.Dismissed && !a.Read {
			if environment != "" && a.Environment != environment {
				continue
			}
			a.Read = true
		}
	}
	return nil
}

func (m *mockAlertStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

// mockServerStore implements store.ServerStore for testing.
type mockServerStore struct {
	mu      sync.Mutex
	servers map[uuid.UUID]*store.Server
	byHost  map[string]uuid.UUID
}

func newMockServerStore() *mockServerStore {
	return &mockServerStore{
		servers: make(map[uuid.UUID]*store.Server),
		byHost:  make(map[string]uuid.UUID),
	}
}

func (m *mockServerStore) Register(ctx context.Context, params store.RegisterServerParams) (*store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id, exists := m.byHost[params.Hostname]; exists {
		s := m.servers[id]
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
	m.servers[s.ID] = s
	m.byHost[params.Hostname] = s.ID
	return s, nil
}

func (m *mockServerStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return s, nil
}

func (m *mockServerStore) List(ctx context.Context) ([]store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Server, 0, len(m.servers))
	for _, s := range m.servers {
		result = append(result, *s)
	}
	return result, nil
}

func (m *mockServerStore) UpdateHeartbeat(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[id]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now()
	s.LastSeenAt = &now
	s.Status = store.ServerOnline
	return nil
}

func (m *mockServerStore) Delete(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[id]
	if !ok {
		return store.ErrNotFound
	}
	delete(m.byHost, s.Hostname)
	delete(m.servers, id)
	return nil
}

func (m *mockServerStore) MarkStaleOffline(ctx context.Context, threshold time.Duration) (int, error) {
	return 0, nil
}

// mockMetricStore implements store.MetricStore for testing.
type mockMetricStore struct {
	mu      sync.Mutex
	metrics []store.MetricPoint
}

func newMockMetricStore() *mockMetricStore {
	return &mockMetricStore{}
}

func (m *mockMetricStore) BatchInsert(ctx context.Context, serverID uuid.UUID, ts time.Time, samples []store.MetricSample) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range samples {
		m.metrics = append(m.metrics, store.MetricPoint{
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

func (m *mockMetricStore) Query(ctx context.Context, params store.MetricQuery) ([]store.MetricPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []store.MetricPoint
	for _, mp := range m.metrics {
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

func (m *mockMetricStore) LatestByServer(ctx context.Context, serverID uuid.UUID) ([]store.MetricPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	latest := make(map[string]store.MetricPoint)
	for _, mp := range m.metrics {
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

func (m *mockMetricStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

// mockUserStore implements store.UserStore for testing.
type mockUserStore struct {
	mu    sync.Mutex
	users map[string]*store.User
	count int
}

func newMockUserStore() *mockUserStore {
	return &mockUserStore{users: make(map[string]*store.User)}
}

func (m *mockUserStore) Create(ctx context.Context, params store.CreateUserParams) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == params.Email {
			return nil, store.ErrEmailTaken
		}
	}
	id := uuid.New().String()
	u := &store.User{
		ID: id, Email: params.Email, PasswordHash: params.PasswordHash,
		DisplayName: params.DisplayName, Role: params.Role,
		IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if params.MCPToken != nil {
		u.MCPToken = params.MCPToken
	}
	m.users[id] = u
	m.count++
	return u, nil
}

func (m *mockUserStore) GetByID(ctx context.Context, id string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return u, nil
}

func (m *mockUserStore) GetByEmail(ctx context.Context, email string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockUserStore) GetByMCPToken(ctx context.Context, token string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.MCPToken != nil && *u.MCPToken == token && u.MCPEnabled && u.IsActive {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockUserStore) List(ctx context.Context) ([]store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []store.User
	for _, u := range m.users {
		result = append(result, *u)
	}
	return result, nil
}

func (m *mockUserStore) Update(ctx context.Context, id string, params store.UpdateUserParams) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if params.DisplayName != nil {
		u.DisplayName = *params.DisplayName
	}
	if params.Role != nil {
		u.Role = *params.Role
	}
	if params.MCPEnabled != nil {
		u.MCPEnabled = *params.MCPEnabled
	}
	if params.IsActive != nil {
		u.IsActive = *params.IsActive
	}
	return u, nil
}

func (m *mockUserStore) UpdatePassword(ctx context.Context, id string, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (m *mockUserStore) UpdateMCPToken(ctx context.Context, id string, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.MCPToken = &token
	return nil
}

func (m *mockUserStore) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.users, id)
	m.count--
	return nil
}

func (m *mockUserStore) Count(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

// mockSessionStore implements store.SessionStore for testing.
type mockSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*store.Session
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{sessions: make(map[string]*store.Session)}
}

func (m *mockSessionStore) Create(ctx context.Context, userID string, token string, expiresAt time.Time) (*store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &store.Session{
		ID: uuid.New().String(), UserID: userID, Token: token,
		ExpiresAt: expiresAt, CreatedAt: time.Now(),
	}
	m.sessions[s.ID] = s
	return s, nil
}

func (m *mockSessionStore) GetByToken(ctx context.Context, token string) (*store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.Token == token && s.ExpiresAt.After(time.Now()) {
			return s, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockSessionStore) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}

func (m *mockSessionStore) DeleteExpired(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for id, s := range m.sessions {
		if s.ExpiresAt.Before(time.Now()) {
			delete(m.sessions, id)
			count++
		}
	}
	return count, nil
}

func (m *mockSessionStore) DeleteAllForUser(ctx context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, id)
		}
	}
	return nil
}

// mockSettingsStore implements store.SettingsStore for testing.
type mockSettingsStore struct {
	mu        sync.Mutex
	retention *store.RetentionSettings
	apiKey    string
}

func newMockSettingsStore() *mockSettingsStore {
	return &mockSettingsStore{}
}

func (m *mockSettingsStore) GetRetention(ctx context.Context) (*store.RetentionSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.retention == nil {
		return &store.RetentionSettings{RetentionDays: 30}, nil
	}
	return m.retention, nil
}

func (m *mockSettingsStore) SetRetention(ctx context.Context, settings store.RetentionSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retention = &settings
	return nil
}

func (m *mockSettingsStore) GetAPIKey(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.apiKey, nil
}

func (m *mockSettingsStore) SetAPIKey(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apiKey = key
	return nil
}
