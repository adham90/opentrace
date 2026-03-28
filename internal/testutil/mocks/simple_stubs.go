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

var _ store.ErrorGroupStore = (*ErrorGroupStore)(nil)
var _ store.ErrorImpactStore = (*ErrorImpactStore)(nil)
var _ store.CodeEntityStore = (*CodeEntityStore)(nil)
var _ store.TraceStore = (*TraceStore)(nil)
var _ store.DeployStore = (*DeployStore)(nil)
var _ store.EventStore = (*EventStore)(nil)
var _ store.MCPActivityStore = (*MCPActivityStore)(nil)
var _ store.AuditStore = (*AuditStore)(nil)
var _ store.WatchStore = (*WatchStore)(nil)
var _ store.HealthCheckStore = (*HealthCheckStore)(nil)
var _ store.AgentNoteStore = (*AgentNoteStore)(nil)
var _ store.TrendStore = (*TrendStore)(nil)
var _ store.AnalyticsStore = (*AnalyticsStore)(nil)
var _ store.ServerStore = (*ServerStore)(nil)
var _ store.MetricStore = (*MetricStore)(nil)
var _ store.InvestigationSessionStore = (*InvestigationSessionStore)(nil)
var _ store.TestCorrelationStore = (*TestCorrelationStore)(nil)
var _ store.QueryMemoryStore = (*QueryMemoryStore)(nil)
var _ store.RunbookEffectivenessStore = (*RunbookEffectivenessStore)(nil)
var _ store.JourneyStore = (*JourneyStore)(nil)
var _ store.WorkflowTemplateStore = (*WorkflowTemplateStore)(nil)
var _ store.ToolTransitionStore = (*ToolTransitionStore)(nil)

// ===========================================================================
// ErrorGroupStore
// ===========================================================================

// ErrorGroupStore is a stub implementing store.ErrorGroupStore.
type ErrorGroupStore struct {
	mu     sync.Mutex
	Groups map[string]*store.ErrorGroup
}

// NewErrorGroupStore returns an initialised ErrorGroupStore stub.
func NewErrorGroupStore() *ErrorGroupStore {
	return &ErrorGroupStore{Groups: make(map[string]*store.ErrorGroup)}
}

func (m *ErrorGroupStore) Upsert(_ context.Context, _ store.LogEntry) error { return nil }
func (m *ErrorGroupStore) Get(_ context.Context, fingerprint string) (*store.ErrorGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.Groups[fingerprint]
	if !ok {
		return nil, store.ErrNotFound
	}
	return g, nil
}
func (m *ErrorGroupStore) List(_ context.Context, _ store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	return nil, nil
}
func (m *ErrorGroupStore) Count(_ context.Context, _ store.ErrorGroupStatus) (int, error) {
	return 0, nil
}
func (m *ErrorGroupStore) Resolve(_ context.Context, _ string, _ string) error { return nil }
func (m *ErrorGroupStore) Ignore(_ context.Context, _ string, _ string) error  { return nil }
func (m *ErrorGroupStore) ListEvents(_ context.Context, _ string, _ int) ([]store.ErrorGroupEvent, error) {
	return nil, nil
}
func (m *ErrorGroupStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// ErrorImpactStore
// ===========================================================================

// ErrorImpactStore is a stub implementing store.ErrorImpactStore.
type ErrorImpactStore struct{}

// NewErrorImpactStore returns an initialised ErrorImpactStore stub.
func NewErrorImpactStore() *ErrorImpactStore { return &ErrorImpactStore{} }

func (m *ErrorImpactStore) TrackImpact(_ context.Context, _ string, _ string, _ map[string]any, _ int64, _ string) error {
	return nil
}
func (m *ErrorImpactStore) GetImpact(_ context.Context, _ string) (*store.ErrorImpact, error) {
	return nil, store.ErrNotFound
}
func (m *ErrorImpactStore) GetAffectedUsers(_ context.Context, _ string, _ int) ([]store.AffectedUser, error) {
	return nil, nil
}
func (m *ErrorImpactStore) GetUserErrors(_ context.Context, _ string, _ time.Time) ([]store.ErrorSummary, error) {
	return nil, nil
}
func (m *ErrorImpactStore) ComputeImpactScores(_ context.Context) error { return nil }
func (m *ErrorImpactStore) TopByImpact(_ context.Context, _ store.ImpactQueryParams) ([]store.ErrorGroupWithImpact, error) {
	return nil, nil
}
func (m *ErrorImpactStore) FindCommonTraits(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}

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

// ===========================================================================
// TraceStore
// ===========================================================================

// TraceStore is a stub implementing store.TraceStore.
type TraceStore struct {
	mu     sync.Mutex
	Traces map[string]*store.TraceStatus
}

// NewTraceStore returns an initialised TraceStore stub.
func NewTraceStore() *TraceStore {
	return &TraceStore{Traces: make(map[string]*store.TraceStatus)}
}

func (m *TraceStore) UpsertTraceStatus(_ context.Context, traceID string, entry store.LogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if traceID == "" {
		return nil
	}
	ts, ok := m.Traces[traceID]
	if !ok {
		ts = &store.TraceStatus{
			TraceID:       traceID,
			SpanCount:     0,
			Services:      []string{},
			FirstSeenAt:   entry.Timestamp,
			LastUpdatedAt: time.Now(),
			Status:        "partial",
		}
		m.Traces[traceID] = ts
	}
	ts.SpanCount++
	ts.LastUpdatedAt = time.Now()
	return nil
}

func (m *TraceStore) GetTraceStatus(_ context.Context, traceID string) (*store.TraceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts, ok := m.Traces[traceID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return ts, nil
}

func (m *TraceStore) ListRecentTraces(_ context.Context, limit, offset int) ([]store.TraceStatus, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []store.TraceStatus
	for _, ts := range m.Traces {
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

func (m *TraceStore) MarkStaleTraces(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}

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

// ===========================================================================
// MCPActivityStore
// ===========================================================================

// MCPActivityStore is a stub implementing store.MCPActivityStore.
type MCPActivityStore struct {
	mu     sync.Mutex
	Events []store.MCPActivityEvent
}

// NewMCPActivityStore returns an initialised MCPActivityStore stub.
func NewMCPActivityStore() *MCPActivityStore {
	return &MCPActivityStore{}
}

func (m *MCPActivityStore) Log(_ context.Context, params store.LogMCPActivityParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, store.MCPActivityEvent{
		SessionID: params.SessionID,
		UserID:    params.UserID,
		ToolName:  params.ToolName,
		IsError:   params.IsError,
		EventType: params.EventType,
		CreatedAt: time.Now(),
	})
	return nil
}

func (m *MCPActivityStore) Stats(_ context.Context) (*store.MCPActivityStats, error) {
	return &store.MCPActivityStats{}, nil
}

func (m *MCPActivityStore) Recent(_ context.Context, limit int) ([]store.MCPActivityEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit > len(m.Events) {
		limit = len(m.Events)
	}
	return m.Events[:limit], nil
}

func (m *MCPActivityStore) ListByInvestigationSession(_ context.Context, _ string) ([]store.MCPActivityEvent, error) {
	return nil, nil
}

func (m *MCPActivityStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *MCPActivityStore) SetSuggestionTracking(_ context.Context, _ string, _ int, _ bool, _ int) error {
	return nil
}

func (m *MCPActivityStore) UpdateFollowedBy(_ context.Context, _ string, _ int, _ string) error {
	return nil
}

// ===========================================================================
// AuditStore
// ===========================================================================

// AuditStore is a stub implementing store.AuditStore.
type AuditStore struct {
	mu      sync.Mutex
	Entries []store.AuditEntry
}

// NewAuditStore returns an initialised AuditStore stub.
func NewAuditStore() *AuditStore {
	return &AuditStore{}
}

func (m *AuditStore) Log(_ context.Context, params store.LogAuditParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Entries = append(m.Entries, store.AuditEntry{
		UserID:     params.UserID,
		UserEmail:  params.UserEmail,
		Action:     params.Action,
		TargetType: params.TargetType,
		TargetID:   params.TargetID,
		Details:    params.Details,
		IPAddress:  params.IPAddress,
		CreatedAt:  time.Now(),
	})
	return nil
}

func (m *AuditStore) Recent(_ context.Context, limit int) ([]store.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit > len(m.Entries) {
		limit = len(m.Entries)
	}
	return m.Entries[:limit], nil
}

func (m *AuditStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// WatchStore
// ===========================================================================

// WatchStore is a stub implementing store.WatchStore.
type WatchStore struct {
	mu      sync.Mutex
	Watches map[string]*store.Watch
}

// NewWatchStore returns an initialised WatchStore stub.
func NewWatchStore() *WatchStore {
	return &WatchStore{Watches: make(map[string]*store.Watch)}
}

func (m *WatchStore) Create(_ context.Context, _ store.CreateWatchParams) (*store.Watch, error) {
	return &store.Watch{ID: "stub-watch"}, nil
}
func (m *WatchStore) GetByID(_ context.Context, id string) (*store.Watch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.Watches[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return w, nil
}
func (m *WatchStore) List(_ context.Context, _ store.ListWatchParams) ([]store.Watch, error) {
	return nil, nil
}
func (m *WatchStore) UpdateStatus(_ context.Context, _ string, _ store.WatchStatus) error {
	return nil
}
func (m *WatchStore) UpdateAfterCheck(_ context.Context, _ string, _ float64, _ int, _ time.Time) error {
	return nil
}
func (m *WatchStore) UpdateBaseline(_ context.Context, _ string, _ *store.WatchBaseline) error {
	return nil
}
func (m *WatchStore) Delete(_ context.Context, _ string) error { return nil }
func (m *WatchStore) GetDueWatches(_ context.Context) ([]store.Watch, error) {
	return nil, nil
}
func (m *WatchStore) ExpireWatches(_ context.Context) (int, error) { return 0, nil }
func (m *WatchStore) CreateRun(_ context.Context, _ string) (*store.WatchRun, error) {
	return &store.WatchRun{ID: "stub-run"}, nil
}
func (m *WatchStore) CompleteRun(_ context.Context, _ string, _ float64, _ bool, _ string) error {
	return nil
}
func (m *WatchStore) FailRun(_ context.Context, _ string, _ string) error { return nil }
func (m *WatchStore) ListRuns(_ context.Context, _ string, _ int) ([]store.WatchRun, error) {
	return nil, nil
}
func (m *WatchStore) CreateAlert(_ context.Context, _ store.CreateWatchAlertParams) (*store.WatchAlert, error) {
	return &store.WatchAlert{ID: "stub-alert"}, nil
}
func (m *WatchStore) GetAlert(_ context.Context, _ string) (*store.WatchAlert, error) {
	return nil, store.ErrNotFound
}
func (m *WatchStore) ListAlerts(_ context.Context, _ string, _ string, _ int) ([]store.WatchAlert, error) {
	return nil, nil
}
func (m *WatchStore) DismissAlert(_ context.Context, _ string, _ string) error     { return nil }
func (m *WatchStore) AcknowledgeAlert(_ context.Context, _ string) error           { return nil }
func (m *WatchStore) CountPendingAlerts(_ context.Context) (int, error)            { return 0, nil }

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

// ===========================================================================
// AgentNoteStore
// ===========================================================================

// AgentNoteStore is a stub implementing store.AgentNoteStore.
type AgentNoteStore struct{}

// NewAgentNoteStore returns an initialised AgentNoteStore stub.
func NewAgentNoteStore() *AgentNoteStore { return &AgentNoteStore{} }

func (m *AgentNoteStore) Upsert(_ context.Context, _, _, _ string) (*store.AgentNote, error) {
	return &store.AgentNote{}, nil
}
func (m *AgentNoteStore) Get(_ context.Context, _, _ string) (*store.AgentNote, error) {
	return nil, store.ErrNotFound
}
func (m *AgentNoteStore) List(_ context.Context, _ string) ([]store.AgentNote, error) {
	return nil, nil
}
func (m *AgentNoteStore) Delete(_ context.Context, _, _ string) error             { return nil }
func (m *AgentNoteStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// TrendStore
// ===========================================================================

// TrendStore is a stub implementing store.TrendStore.
type TrendStore struct{}

// NewTrendStore returns an initialised TrendStore stub.
func NewTrendStore() *TrendStore { return &TrendStore{} }

func (m *TrendStore) AggregateBuckets(_ context.Context, _ string, _ time.Time) error { return nil }
func (m *TrendStore) QueryTrends(_ context.Context, _ store.TrendQueryParams) ([]store.MetricBucket, error) {
	return nil, nil
}
func (m *TrendStore) ListDeployMarkers(_ context.Context, _ string, _ time.Time) ([]store.DeployMarker, error) {
	return nil, nil
}
func (m *TrendStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// AnalyticsStore
// ===========================================================================

// AnalyticsStore is a stub implementing store.AnalyticsStore.
type AnalyticsStore struct{}

// NewAnalyticsStore returns an initialised AnalyticsStore stub.
func NewAnalyticsStore() *AnalyticsStore { return &AnalyticsStore{} }

func (m *AnalyticsStore) AggregateEndpointStats(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (m *AnalyticsStore) UpdateTrafficHeatmap(_ context.Context, _ time.Time) error { return nil }
func (m *AnalyticsStore) TopEndpoints(_ context.Context, _ store.TopEndpointParams) ([]store.EndpointStat, error) {
	return nil, nil
}
func (m *AnalyticsStore) TrafficSummary(_ context.Context, _ store.AnalyticsParams) (*store.TrafficSummary, error) {
	return &store.TrafficSummary{}, nil
}
func (m *AnalyticsStore) TrafficHeatmap(_ context.Context, _ string) ([]store.HeatmapCell, error) {
	return nil, nil
}
func (m *AnalyticsStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

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

// ===========================================================================
// InvestigationSessionStore
// ===========================================================================

// InvestigationSessionStore is a stub implementing store.InvestigationSessionStore.
type InvestigationSessionStore struct{}

// NewInvestigationSessionStore returns an initialised InvestigationSessionStore stub.
func NewInvestigationSessionStore() *InvestigationSessionStore {
	return &InvestigationSessionStore{}
}

func (m *InvestigationSessionStore) Create(_ context.Context, _ store.CreateInvestigationSessionParams) (*store.InvestigationSession, error) {
	return &store.InvestigationSession{ID: "test-session"}, nil
}
func (m *InvestigationSessionStore) GetByID(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, store.ErrNotFound
}
func (m *InvestigationSessionStore) Close(_ context.Context, _ string) error { return nil }
func (m *InvestigationSessionStore) Update(_ context.Context, _ string, _ store.UpdateInvestigationSessionParams) error {
	return nil
}
func (m *InvestigationSessionStore) FindRecent(_ context.Context, _ store.FindRecentSessionParams) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) List(_ context.Context, _ store.ListInvestigationSessionParams) ([]store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) Stats(_ context.Context) (*store.InvestigationSessionStats, error) {
	return &store.InvestigationSessionStats{}, nil
}
func (m *InvestigationSessionStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
func (m *InvestigationSessionStore) RecordStep(_ context.Context, _ string, _ string, _ bool) error {
	return nil
}
func (m *InvestigationSessionStore) FindByCreatedWatcher(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) FindByResolvedError(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) FindByCreatedHealthcheck(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) FindSimilar(_ context.Context, _ store.FindSimilarParams) ([]store.InvestigationSession, error) {
	return nil, nil
}

// ===========================================================================
// TestCorrelationStore
// ===========================================================================

// TestCorrelationStore is a stub implementing store.TestCorrelationStore.
type TestCorrelationStore struct{}

// NewTestCorrelationStore returns an initialised TestCorrelationStore stub.
func NewTestCorrelationStore() *TestCorrelationStore { return &TestCorrelationStore{} }

func (m *TestCorrelationStore) RefreshUncoveredPaths(_ context.Context) error { return nil }
func (m *TestCorrelationStore) TopByPriority(_ context.Context, _ string, _ int) ([]store.UncoveredErrorPath, error) {
	return nil, nil
}
func (m *TestCorrelationStore) GetByFingerprint(_ context.Context, _ string) (*store.UncoveredErrorPath, error) {
	return nil, store.ErrNotFound
}
func (m *TestCorrelationStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// ===========================================================================
// QueryMemoryStore
// ===========================================================================

// QueryMemoryStore is a stub implementing store.QueryMemoryStore.
type QueryMemoryStore struct{}

// NewQueryMemoryStore returns an initialised QueryMemoryStore stub.
func NewQueryMemoryStore() *QueryMemoryStore { return &QueryMemoryStore{} }

func (m *QueryMemoryStore) Get(_ context.Context, _ string) (*store.QueryMemory, error) {
	return nil, store.ErrNotFound
}
func (m *QueryMemoryStore) Upsert(_ context.Context, _ store.UpsertQueryMemoryParams) error {
	return nil
}
func (m *QueryMemoryStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// ===========================================================================
// RunbookEffectivenessStore
// ===========================================================================

// RunbookEffectivenessStore is a stub implementing store.RunbookEffectivenessStore.
type RunbookEffectivenessStore struct{}

// NewRunbookEffectivenessStore returns an initialised RunbookEffectivenessStore stub.
func NewRunbookEffectivenessStore() *RunbookEffectivenessStore {
	return &RunbookEffectivenessStore{}
}

func (m *RunbookEffectivenessStore) RecordExecution(_ context.Context, _ string) error { return nil }
func (m *RunbookEffectivenessStore) UpdateOutcome(_ context.Context, _ store.UpdateRunbookEffectivenessParams) error {
	return nil
}
func (m *RunbookEffectivenessStore) GetMostEffective(_ context.Context) (*store.RunbookEffectiveness, error) {
	return nil, nil
}
func (m *RunbookEffectivenessStore) List(_ context.Context) ([]store.RunbookEffectiveness, error) {
	return nil, nil
}

// ===========================================================================
// JourneyStore
// ===========================================================================

// JourneyStore is a stub implementing store.JourneyStore.
type JourneyStore struct{}

// NewJourneyStore returns an initialised JourneyStore stub.
func NewJourneyStore() *JourneyStore { return &JourneyStore{} }

func (m *JourneyStore) BuildSessions(_ context.Context, _ time.Time) error { return nil }
func (m *JourneyStore) GetSession(_ context.Context, _ string) (*store.UserSession, error) {
	return nil, store.ErrNotFound
}
func (m *JourneyStore) ListSessions(_ context.Context, _ store.SessionListParams) ([]store.UserSession, error) {
	return nil, nil
}
func (m *JourneyStore) GetSessionRequests(_ context.Context, _ string) ([]store.RequestStep, error) {
	return nil, nil
}
func (m *JourneyStore) GetUserJourney(_ context.Context, _ string, _ time.Time, _ int) ([]store.RequestStep, error) {
	return nil, nil
}
func (m *JourneyStore) CommonPaths(_ context.Context, _ store.PathAnalysisParams) ([]store.PathFrequency, error) {
	return nil, nil
}
func (m *JourneyStore) CreateFunnel(_ context.Context, f store.Funnel) (*store.Funnel, error) {
	f.ID = 1
	return &f, nil
}
func (m *JourneyStore) GetFunnel(_ context.Context, _ int64) (*store.Funnel, error) {
	return nil, store.ErrNotFound
}
func (m *JourneyStore) ListFunnels(_ context.Context) ([]store.Funnel, error) { return nil, nil }
func (m *JourneyStore) AnalyzeFunnel(_ context.Context, _ int64, _ time.Time) (*store.FunnelResult, error) {
	return &store.FunnelResult{}, nil
}
func (m *JourneyStore) DeleteFunnel(_ context.Context, _ int64) error { return nil }
func (m *JourneyStore) GetRequestTimeline(_ context.Context, _ int64) (*store.RequestTimeline, error) {
	return nil, store.ErrNotFound
}
func (m *JourneyStore) GetSessionTimeline(_ context.Context, _ string) ([]store.RequestTimeline, error) {
	return nil, nil
}

// ===========================================================================
// WorkflowTemplateStore
// ===========================================================================

// WorkflowTemplateStore is a stub implementing store.WorkflowTemplateStore.
type WorkflowTemplateStore struct{}

// NewWorkflowTemplateStore returns an initialised WorkflowTemplateStore stub.
func NewWorkflowTemplateStore() *WorkflowTemplateStore { return &WorkflowTemplateStore{} }

func (m *WorkflowTemplateStore) Seed(_ context.Context, _ []store.WorkflowTemplate) error {
	return nil
}
func (m *WorkflowTemplateStore) GetNextStep(_ context.Context, _ string, _ int) ([]store.WorkflowTemplate, error) {
	return nil, nil
}
func (m *WorkflowTemplateStore) GetByName(_ context.Context, _ string) ([]store.WorkflowTemplate, error) {
	return nil, nil
}
func (m *WorkflowTemplateStore) List(_ context.Context, _ string) ([]store.WorkflowTemplate, error) {
	return nil, nil
}

// ===========================================================================
// ToolTransitionStore
// ===========================================================================

// ToolTransitionStore is a stub implementing store.ToolTransitionStore.
type ToolTransitionStore struct{}

// NewToolTransitionStore returns an initialised ToolTransitionStore stub.
func NewToolTransitionStore() *ToolTransitionStore { return &ToolTransitionStore{} }

func (m *ToolTransitionStore) Increment(_ context.Context, _, _, _ string) error { return nil }
func (m *ToolTransitionStore) IncrementWithOutcome(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (m *ToolTransitionStore) GetTransitions(_ context.Context, _ store.GetTransitionsParams) ([]store.ToolTransition, error) {
	return nil, nil
}
func (m *ToolTransitionStore) GetDeadEnds(_ context.Context, _ string) ([]store.ToolTransition, error) {
	return nil, nil
}
func (m *ToolTransitionStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
