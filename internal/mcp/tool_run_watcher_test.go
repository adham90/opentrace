package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// mockRunStoreForExec extends mockWatcherRunStore with a Create that returns a valid run.
type mockRunStoreForExec struct {
	mockWatcherRunStore
}

func (m *mockRunStoreForExec) Create(_ context.Context, watcherID uuid.UUID) (*store.WatcherRun, error) {
	return &store.WatcherRun{
		ID:        uuid.New(),
		WatcherID: watcherID,
		Status:    "running",
		StartedAt: time.Now(),
	}, nil
}

func newTestExecutor(ws store.WatcherStore) *watcher.Executor {
	registry := connector.NewRegistry()
	runStore := &mockRunStoreForExec{}
	alertStore := &mockAlertStore{}
	providerCache := llm.NewProviderCache(nil, nil)
	return watcher.NewExecutor(ws, runStore, alertStore, registry, providerCache, agent.RunConfig{}, watcher.NewEventHub())
}

func TestRunWatcherNowHandler_Success(t *testing.T) {
	wID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: wID, Title: "Test Watcher", Status: store.WatcherActive, WatcherType: store.WatcherTypeAI},
		},
	}

	executor := newTestExecutor(ws)

	handler := runWatcherNowHandler(ws, executor)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": wID.String(),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "triggered") {
		t.Errorf("expected 'triggered' in response, got: %s", text)
	}
	if !contains(text, "Test Watcher") {
		t.Errorf("expected watcher title in response, got: %s", text)
	}
}

func TestRunWatcherNowHandler_NotFound(t *testing.T) {
	ws := &mockWatcherStore{}
	executor := newTestExecutor(ws)

	handler := runWatcherNowHandler(ws, executor)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": uuid.New().String(),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for non-existent watcher")
	}
	text := resultText(t, result)
	if !contains(text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text)
	}
}

func TestRunWatcherNowHandler_MissingID(t *testing.T) {
	ws := &mockWatcherStore{}
	executor := newTestExecutor(ws)

	handler := runWatcherNowHandler(ws, executor)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing watcher_id")
	}
}

func TestRunWatcherNowHandler_InvalidUUID(t *testing.T) {
	ws := &mockWatcherStore{}
	executor := newTestExecutor(ws)

	handler := runWatcherNowHandler(ws, executor)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": "not-a-uuid",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid UUID")
	}
}
