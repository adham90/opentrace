package watcher

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

// schedulerWatcherStore extends mockWatcherStore with GetDueWatchers support.
type schedulerWatcherStore struct {
	mockWatcherStore
	dueWatchers []store.Watcher
	callCount   int
}

func (s *schedulerWatcherStore) GetDueWatchers(ctx context.Context) ([]store.Watcher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount++
	// Only return watchers on the first call to avoid infinite execution
	if s.callCount == 1 {
		return s.dueWatchers, nil
	}
	return nil, nil
}
func (s *schedulerWatcherStore) UpdateAdaptiveState(ctx context.Context, id uuid.UUID, params store.UpdateAdaptiveParams) error {
	return nil
}
func (s *schedulerWatcherStore) ResumeWatcher(ctx context.Context, id uuid.UUID) error {
	return nil
}

func TestScheduler_StartStop(t *testing.T) {
	ws := &schedulerWatcherStore{
		mockWatcherStore: *newMockWatcherStore(),
	}
	rs := newMockRunStore()
	as := newMockAlertStore()
	registry := connector.NewRegistry()

	sched := NewScheduler(SchedulerOpts{
		WatcherStore:  ws,
		RunStore:      rs,
		AlertStore:    as,
		Registry:      registry,
		ProviderCache: newTestProviderCache(&mockLLMProvider{response: llm.ChatResponse{Content: "OK: all good"}}),
		AgentCfg:      agent.RunConfig{MaxSteps: 3, MaxToolCalls: 2, MaxObservationBytes: 4096},
		PollInterval:  50 * time.Millisecond,
		MaxConcurrent: 2,
	})

	ctx := context.Background()
	sched.Start(ctx)

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)

	sched.Stop()

	// Verify it polled at least once
	ws.mu.Lock()
	calls := ws.callCount
	ws.mu.Unlock()
	if calls < 1 {
		t.Errorf("expected at least 1 poll, got %d", calls)
	}
}

func TestScheduler_ExecutesDueWatchers(t *testing.T) {
	watcherID := uuid.New()
	ws := &schedulerWatcherStore{
		mockWatcherStore: *newMockWatcherStore(),
		dueWatchers: []store.Watcher{
			{
				ID:              watcherID,
				Title:           "Test watcher",
				Description:     "For scheduler test",
				Severity:        store.SeverityWarning,
				Filters:         json.RawMessage(`{}`),
				TimeRange:       "5m",
				Notify:          json.RawMessage(`["dashboard"]`),
			},
		},
	}
	rs := newMockRunStore()
	as := newMockAlertStore()
	registry := connector.NewRegistry()

	sched := NewScheduler(SchedulerOpts{
		WatcherStore:  ws,
		RunStore:      rs,
		AlertStore:    as,
		Registry:      registry,
		ProviderCache: newTestProviderCache(&mockLLMProvider{response: llm.ChatResponse{Content: "ALERT: found issues"}}),
		AgentCfg:      agent.RunConfig{MaxSteps: 3, MaxToolCalls: 2, MaxObservationBytes: 4096},
		PollInterval:  50 * time.Millisecond,
		MaxConcurrent: 1,
	})

	ctx := context.Background()
	sched.Start(ctx)

	// Wait for execution
	time.Sleep(200 * time.Millisecond)
	sched.Stop()

	// Verify the watcher was executed
	runs := rs.getRuns(watcherID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	alerts := as.getAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}

func TestScheduler_ContextCancellation(t *testing.T) {
	ws := &schedulerWatcherStore{
		mockWatcherStore: *newMockWatcherStore(),
	}
	rs := newMockRunStore()
	as := newMockAlertStore()
	registry := connector.NewRegistry()

	sched := NewScheduler(SchedulerOpts{
		WatcherStore:  ws,
		RunStore:      rs,
		AlertStore:    as,
		Registry:      registry,
		ProviderCache: newTestProviderCache(&mockLLMProvider{response: llm.ChatResponse{Content: "OK: fine"}}),
		AgentCfg:      agent.RunConfig{MaxSteps: 3, MaxToolCalls: 2, MaxObservationBytes: 4096},
		PollInterval:  1 * time.Second, // long poll to test cancellation
		MaxConcurrent: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)

	// Cancel immediately
	cancel()
	sched.Stop()

	// Should not hang — test passes if it completes
}
