package watcher

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

// newTestProviderCache creates a ProviderCache backed by the given mock LLM as default.
func newTestProviderCache(mockLLM llm.LLMProvider) *llm.ProviderCache {
	return llm.NewProviderCache(&config.Config{}, mockLLM)
}

func TestExecutor_AlertTriggered(t *testing.T) {
	ws := newMockWatcherStore()
	rs := newMockRunStore()
	as := newMockAlertStore()
	registry := connector.NewRegistry()

	// Mock LLM that always returns an ALERT response
	mockLLM := &mockLLMProvider{
		response: llm.ChatResponse{
			Content: "ALERT: Found 47 errors in payment-api in the last 15 minutes. Error rate is 3x normal.",
		},
	}

	exec := NewExecutor(ws, rs, as, registry, newTestProviderCache(mockLLM), agent.RunConfig{
		MaxSteps:            5,
		MaxToolCalls:        3,
		MaxObservationBytes: 4096,
	}, nil)

	w := store.Watcher{
		ID:              uuid.New(),
		Title:           "Payment errors",
		Description:     "Watch for payment errors",
		Severity:        store.SeverityCritical,
		Filters:         json.RawMessage(`{"service":"payment-api"}`),
		TimeRange:       "5m",
		Notify:          json.RawMessage(`["dashboard"]`),
	}

	exec.Execute(context.Background(), w)

	// Verify run was created and completed
	runs := rs.getRuns(w.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != "completed" {
		t.Errorf("run status = %q, want completed", runs[0].Status)
	}
	if !runs[0].HasAlert {
		t.Error("expected has_alert = true")
	}

	// Verify alert was created
	alerts := as.getAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != store.SeverityCritical {
		t.Errorf("alert severity = %q, want critical", alerts[0].Severity)
	}

	// Verify watcher timing was updated
	lastRun, nextRun := ws.getRunTime(w.ID)
	if lastRun.IsZero() {
		t.Error("expected last_run_at to be set")
	}
	if nextRun.IsZero() {
		t.Error("expected next_run_at to be set")
	}
}

func TestExecutor_CronSchedule(t *testing.T) {
	ws := newMockWatcherStore()
	rs := newMockRunStore()
	as := newMockAlertStore()
	registry := connector.NewRegistry()

	mockLLM := &mockLLMProvider{
		response: llm.ChatResponse{
			Content: "OK: Everything looks normal.",
		},
	}

	exec := NewExecutor(ws, rs, as, registry, newTestProviderCache(mockLLM), agent.RunConfig{
		MaxSteps:            5,
		MaxToolCalls:        3,
		MaxObservationBytes: 4096,
	}, nil)

	w := store.Watcher{
		ID:          uuid.New(),
		Title:       "Daily check",
		Description: "Check health daily",
		Severity:    store.SeverityInfo,
		Filters:     json.RawMessage(`{}`),
		TimeRange:   "1h",
		Schedule:    "0 9 * * *",
		Notify:      json.RawMessage(`["dashboard"]`),
	}

	exec.Execute(context.Background(), w)

	// Verify watcher timing uses cron schedule, not interval
	_, nextRun := ws.getRunTime(w.ID)
	if nextRun.IsZero() {
		t.Fatal("expected next_run_at to be set")
	}

	// Next run should be at 9:00 AM, not now+1h
	if nextRun.Hour() != 9 || nextRun.Minute() != 0 {
		t.Errorf("expected next_run at 09:00, got %02d:%02d", nextRun.Hour(), nextRun.Minute())
	}
}

func TestExecutor_NoAlert(t *testing.T) {
	ws := newMockWatcherStore()
	rs := newMockRunStore()
	as := newMockAlertStore()
	registry := connector.NewRegistry()

	mockLLM := &mockLLMProvider{
		response: llm.ChatResponse{
			Content: "OK: Everything looks normal. No errors in the last 15 minutes.",
		},
	}

	exec := NewExecutor(ws, rs, as, registry, newTestProviderCache(mockLLM), agent.RunConfig{
		MaxSteps:            5,
		MaxToolCalls:        3,
		MaxObservationBytes: 4096,
	}, nil)

	w := store.Watcher{
		ID:              uuid.New(),
		Title:           "Health check",
		Description:     "Check system health",
		Severity:        store.SeverityInfo,
		Filters:         json.RawMessage(`{}`),
		TimeRange:       "10m",
		Notify:          json.RawMessage(`["dashboard"]`),
	}

	exec.Execute(context.Background(), w)

	runs := rs.getRuns(w.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].HasAlert {
		t.Error("expected has_alert = false")
	}

	alerts := as.getAlerts()
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alerts))
	}
}

func TestExecutor_AgentError(t *testing.T) {
	ws := newMockWatcherStore()
	rs := newMockRunStore()
	as := newMockAlertStore()
	registry := connector.NewRegistry()

	// Mock LLM that returns empty (triggers agent error)
	mockLLM := &mockLLMProvider{
		response: llm.ChatResponse{Content: ""},
	}

	exec := NewExecutor(ws, rs, as, registry, newTestProviderCache(mockLLM), agent.RunConfig{
		MaxSteps:            5,
		MaxToolCalls:        3,
		MaxObservationBytes: 4096,
	}, nil)

	w := store.Watcher{
		ID:              uuid.New(),
		Title:           "Failing watcher",
		Description:     "This will fail",
		Severity:        store.SeverityWarning,
		Filters:         json.RawMessage(`{}`),
		TimeRange:       "5m",
		Notify:          json.RawMessage(`["dashboard"]`),
	}

	exec.Execute(context.Background(), w)

	runs := rs.getRuns(w.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != "error" {
		t.Errorf("run status = %q, want error", runs[0].Status)
	}

	alerts := as.getAlerts()
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts on error, got %d", len(alerts))
	}
}

// --- Mock implementations ---

type mockLLMProvider struct {
	response llm.ChatResponse
}

func (m *mockLLMProvider) ChatCompletion(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return m.response, nil
}

// mockWatcherStore tracks UpdateRunTime calls
type mockWatcherStore struct {
	mu       sync.Mutex
	lastRuns map[uuid.UUID]time.Time
	nextRuns map[uuid.UUID]time.Time
}

func newMockWatcherStore() *mockWatcherStore {
	return &mockWatcherStore{
		lastRuns: make(map[uuid.UUID]time.Time),
		nextRuns: make(map[uuid.UUID]time.Time),
	}
}

func (m *mockWatcherStore) Create(ctx context.Context, params store.CreateWatcherParams) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) List(ctx context.Context, params store.ListWatcherParams) ([]store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateWatcherParams) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) UpdateStatus(ctx context.Context, id uuid.UUID, status store.WatcherStatus) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) Delete(ctx context.Context, id uuid.UUID) error { return nil }
func (m *mockWatcherStore) GetDueWatchers(ctx context.Context) ([]store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) UpdateRunTime(ctx context.Context, id uuid.UUID, lastRun, nextRun time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastRuns[id] = lastRun
	m.nextRuns[id] = nextRun
	return nil
}
func (m *mockWatcherStore) getRunTime(id uuid.UUID) (time.Time, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRuns[id], m.nextRuns[id]
}
func (m *mockWatcherStore) UpdateAdaptiveState(ctx context.Context, id uuid.UUID, params store.UpdateAdaptiveParams) error {
	return nil
}
func (m *mockWatcherStore) ResumeMonitor(ctx context.Context, id uuid.UUID) error {
	return nil
}

// mockRunStore tracks created runs and their status updates
type mockRunStore struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*store.WatcherRun // by run ID
	byW  map[uuid.UUID][]uuid.UUID       // watcher ID -> run IDs
}

func newMockRunStore() *mockRunStore {
	return &mockRunStore{
		runs: make(map[uuid.UUID]*store.WatcherRun),
		byW:  make(map[uuid.UUID][]uuid.UUID),
	}
}

func (m *mockRunStore) Create(ctx context.Context, watcherID uuid.UUID) (*store.WatcherRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := &store.WatcherRun{
		ID:        uuid.New(),
		WatcherID: watcherID,
		StartedAt: time.Now(),
		Status:    "running",
		CreatedAt: time.Now(),
	}
	m.runs[r.ID] = r
	m.byW[watcherID] = append(m.byW[watcherID], r.ID)
	return r, nil
}

func (m *mockRunStore) Complete(ctx context.Context, id uuid.UUID, summary string, details any, hasAlert bool) error {
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

func (m *mockRunStore) Fail(ctx context.Context, id uuid.UUID, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil {
		return store.ErrNotFound
	}
	now := time.Now()
	r.Status = "error"
	r.Error = &errMsg
	r.FinishedAt = &now
	return nil
}

func (m *mockRunStore) FailStaleRuns(ctx context.Context, olderThan time.Duration) (int, error) {
	return 0, nil
}

func (m *mockRunStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockRunStore) CountRuns(ctx context.Context, params store.CountRunParams) (int, error) {
	return 0, nil
}

func (m *mockRunStore) List(ctx context.Context, watcherID uuid.UUID, limit int) ([]store.WatcherRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.byW[watcherID]
	var result []store.WatcherRun
	for _, id := range ids {
		if r := m.runs[id]; r != nil {
			result = append(result, *r)
		}
	}
	return result, nil
}

func (m *mockRunStore) ListWithFilter(ctx context.Context, watcherID uuid.UUID, limit int, status string) ([]store.WatcherRun, error) {
	return m.List(ctx, watcherID, limit)
}

func (m *mockRunStore) GetByID(ctx context.Context, id uuid.UUID) (*store.WatcherRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil {
		return nil, store.ErrNotFound
	}
	return r, nil
}

func (m *mockRunStore) getRuns(watcherID uuid.UUID) []store.WatcherRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.byW[watcherID]
	var result []store.WatcherRun
	for _, id := range ids {
		if r := m.runs[id]; r != nil {
			result = append(result, *r)
		}
	}
	return result
}

// mockAlertStore tracks created alerts
type mockAlertStore struct {
	mu     sync.Mutex
	alerts []store.Alert
}

func newMockAlertStore() *mockAlertStore {
	return &mockAlertStore{}
}

func (m *mockAlertStore) Create(ctx context.Context, params store.CreateAlertParams) (*store.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := store.Alert{
		ID:        uuid.New(),
		WatcherID: params.WatcherID,
		RunID:     params.RunID,
		Title:     params.Title,
		Summary:   params.Summary,
		Severity:  params.Severity,
		Details:   params.Details,
		CreatedAt: time.Now(),
	}
	m.alerts = append(m.alerts, a)
	return &a, nil
}

func (m *mockAlertStore) List(ctx context.Context, params store.ListAlertParams) ([]store.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alerts, nil
}

func (m *mockAlertStore) CountUnread(ctx context.Context, environment string) (int, error) {
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

func (m *mockAlertStore) CountTotal(ctx context.Context, environment string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.alerts), nil
}

func (m *mockAlertStore) MarkRead(ctx context.Context, id uuid.UUID) error      { return nil }
func (m *mockAlertStore) MarkAllRead(ctx context.Context, environment string) error { return nil }
func (m *mockAlertStore) Dismiss(ctx context.Context, id uuid.UUID) error          { return nil }
func (m *mockAlertStore) DismissAll(ctx context.Context, environment string) error { return nil }
func (m *mockAlertStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockAlertStore) CountBySeverity(ctx context.Context, since, until time.Time, environment string) (map[string]int, error) {
	return make(map[string]int), nil
}

func (m *mockAlertStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.alerts {
		if m.alerts[i].ID == id {
			return &m.alerts[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockAlertStore) getAlerts() []store.Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Alert, len(m.alerts))
	copy(result, m.alerts)
	return result
}
