package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/google/uuid"
)

// withAdmin injects an admin user into the request context so auth middleware passes.
func withAdmin(req *http.Request) *http.Request {
	admin := &store.User{ID: "test-admin", Email: "admin@test.com", Role: store.RoleAdmin, IsActive: true, DisplayName: "Admin"}
	return req.WithContext(server.WithUser(req.Context(), admin))
}

// mockLogStore implements store.LogStore for testing.
type mockLogStore struct {
	mu               sync.Mutex
	entries          []store.LogEntry
	err              error
	lastSearchParams store.LogSearchParams
	batches          map[string]*store.BatchRecord
}

func newMockLogStore() *mockLogStore {
	return &mockLogStore{batches: make(map[string]*store.BatchRecord)}
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

func (m *mockLogStore) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return nil, nil
}

func (m *mockLogStore) Histogram(_ context.Context, _ store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}

func (m *mockLogStore) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}

func (m *mockLogStore) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *mockLogStore) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *mockLogStore) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) {
	return nil, store.ErrNotFound
}

func (m *mockLogStore) RecordBatch(_ context.Context, batchID string, logCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches[batchID] = &store.BatchRecord{BatchID: batchID, LogCount: logCount, ReceivedAt: time.Now()}
	return nil
}

func (m *mockLogStore) GetBatch(_ context.Context, batchID string) (*store.BatchRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.batches[batchID]
	if !ok {
		return nil, nil
	}
	return rec, nil
}

func (m *mockLogStore) PruneBatches(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

func (m *mockLogStore) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}

func (m *mockLogStore) AggregateRequestSummaries(_ context.Context, _ store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return &store.RequestSummaryAggregates{}, nil
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

func (m *mockServerStore) List(ctx context.Context, params store.ListServerParams) ([]store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Server, 0, len(m.servers))
	for _, s := range m.servers {
		result = append(result, *s)
	}
	// Apply pagination
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

func (m *mockServerStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateServerParams) (*store.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if params.DisplayName != nil {
		s.DisplayName = *params.DisplayName
	}
	s.UpdatedAt = time.Now()
	return s, nil
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
	mu        sync.Mutex
	users     map[string]*store.User
	count     int
	createErr error // if set, Create returns this error
}

func newMockUserStore() *mockUserStore {
	return &mockUserStore{users: make(map[string]*store.User)}
}

func (m *mockUserStore) Create(ctx context.Context, params store.CreateUserParams) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
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
	mu               sync.Mutex
	retention        *store.RetentionSettings
	apiKey           string
	corsOrigins      string
	maxQueryRows     int
	statementTimeout int
	mcpName          string
	samplingRules    []store.SamplingRule
	apiKeyErr        error // if set, GetAPIKey returns this error
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
	if m.apiKeyErr != nil {
		return "", m.apiKeyErr
	}
	return m.apiKey, nil
}

func (m *mockSettingsStore) SetAPIKey(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apiKey = key
	return nil
}

func (m *mockSettingsStore) GetCORSOrigins(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.corsOrigins, nil
}

func (m *mockSettingsStore) SetCORSOrigins(ctx context.Context, origins string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.corsOrigins = origins
	return nil
}

func (m *mockSettingsStore) GetMaxQueryRows(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxQueryRows, nil
}

func (m *mockSettingsStore) SetMaxQueryRows(ctx context.Context, val int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxQueryRows = val
	return nil
}

func (m *mockSettingsStore) GetStatementTimeout(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statementTimeout, nil
}

func (m *mockSettingsStore) SetStatementTimeout(ctx context.Context, val int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statementTimeout = val
	return nil
}

func (m *mockSettingsStore) GetMCPName(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mcpName, nil
}

func (m *mockSettingsStore) SetMCPName(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mcpName = name
	return nil
}

func (m *mockSettingsStore) GetSamplingRules(ctx context.Context) ([]store.SamplingRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.samplingRules, nil
}

func (m *mockSettingsStore) SetSamplingRules(ctx context.Context, rules []store.SamplingRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samplingRules = rules
	return nil
}

func (m *mockSettingsStore) GetTelegramConfig(_ context.Context) (*store.TelegramConfig, error) {
	return &store.TelegramConfig{}, nil
}

func (m *mockSettingsStore) SetTelegramConfig(_ context.Context, _ store.TelegramConfig) error {
	return nil
}

func (m *mockSettingsStore) GetSlackConfig(_ context.Context) (*store.SlackConfig, error) {
	return &store.SlackConfig{}, nil
}

func (m *mockSettingsStore) SetSlackConfig(_ context.Context, _ store.SlackConfig) error {
	return nil
}

// mockMCPActivityStore implements store.MCPActivityStore for testing.
type mockMCPActivityStore struct {
	mu     sync.Mutex
	events []store.MCPActivityEvent
}

func newMockMCPActivityStore() *mockMCPActivityStore {
	return &mockMCPActivityStore{}
}

func (m *mockMCPActivityStore) Log(_ context.Context, params store.LogMCPActivityParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, store.MCPActivityEvent{
		SessionID: params.SessionID,
		UserID:    params.UserID,
		ToolName:  params.ToolName,
		IsError:   params.IsError,
		EventType: params.EventType,
		CreatedAt: time.Now(),
	})
	return nil
}

func (m *mockMCPActivityStore) Stats(_ context.Context) (*store.MCPActivityStats, error) {
	return &store.MCPActivityStats{}, nil
}

func (m *mockMCPActivityStore) Recent(_ context.Context, limit int) ([]store.MCPActivityEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit > len(m.events) {
		limit = len(m.events)
	}
	return m.events[:limit], nil
}

func (m *mockMCPActivityStore) ListByInvestigationSession(_ context.Context, _ string) ([]store.MCPActivityEvent, error) {
	return nil, nil
}

func (m *mockMCPActivityStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockMCPActivityStore) SetSuggestionTracking(_ context.Context, _ string, _ int, _ bool, _ int) error {
	return nil
}

func (m *mockMCPActivityStore) UpdateFollowedBy(_ context.Context, _ string, _ int, _ string) error {
	return nil
}

// mockTraceStore implements store.TraceStore for testing.
type mockTraceStore struct {
	mu     sync.Mutex
	traces map[string]*store.TraceStatus
}

func newMockTraceStore() *mockTraceStore {
	return &mockTraceStore{traces: make(map[string]*store.TraceStatus)}
}

func (m *mockTraceStore) UpsertTraceStatus(_ context.Context, traceID string, entry store.LogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if traceID == "" {
		return nil
	}
	ts, ok := m.traces[traceID]
	if !ok {
		ts = &store.TraceStatus{
			TraceID:       traceID,
			SpanCount:     0,
			Services:      []string{},
			FirstSeenAt:   entry.Timestamp,
			LastUpdatedAt: time.Now(),
			Status:        "partial",
		}
		m.traces[traceID] = ts
	}
	ts.SpanCount++
	ts.LastUpdatedAt = time.Now()
	return nil
}

func (m *mockTraceStore) GetTraceStatus(_ context.Context, traceID string) (*store.TraceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts, ok := m.traces[traceID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return ts, nil
}

func (m *mockTraceStore) ListRecentTraces(_ context.Context, limit, offset int) ([]store.TraceStatus, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []store.TraceStatus
	for _, ts := range m.traces {
		result = append(result, *ts)
	}
	total := len(result)
	if offset > len(result) {
		offset = len(result)
	}
	result = result[offset:]
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}
	return result, total, nil
}

func (m *mockTraceStore) MarkStaleTraces(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}

// mockCodeEntityStore implements store.CodeEntityStore for testing.
type mockCodeEntityStore struct{}

func (m *mockCodeEntityStore) Upsert(_ context.Context, _ store.UpsertCodeEntityParams) (*store.CodeEntity, error) {
	return &store.CodeEntity{}, nil
}
func (m *mockCodeEntityStore) GetByName(_ context.Context, _ store.CodeEntityType, _, _ string) (*store.CodeEntity, error) {
	return nil, store.ErrNotFound
}
func (m *mockCodeEntityStore) TopByRisk(_ context.Context, _ string, _ int) ([]store.CodeEntity, error) {
	return nil, nil
}
func (m *mockCodeEntityStore) BatchGetRisk(_ context.Context, _ store.CodeEntityType, _ []string, _ string) ([]store.CodeEntity, error) {
	return nil, nil
}
func (m *mockCodeEntityStore) IncrementError(_ context.Context, _ store.CodeEntityType, _, _ string) error {
	return nil
}
func (m *mockCodeEntityStore) IncrementInvestigation(_ context.Context, _ store.CodeEntityType, _, _ string) error {
	return nil
}
func (m *mockCodeEntityStore) BatchRecomputeRisk(_ context.Context) error { return nil }
func (m *mockCodeEntityStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
