package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

func TestWatcherRunHistoryHandler_Success(t *testing.T) {
	watcherID := uuid.New()
	now := time.Now().UTC()
	finished := now.Add(-1 * time.Minute)

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Connection Watcher", Status: store.WatcherActive, WatcherType: store.WatcherTypeRule, Schedule: "*/5 * * * *", TimeRange: "5m"},
		},
	}
	rs := &mockWatcherRunStore{
		runs: []store.WatcherRun{
			{ID: uuid.New(), WatcherID: watcherID, StartedAt: now.Add(-2 * time.Minute), FinishedAt: &finished, Status: "completed", HasAlert: true},
			{ID: uuid.New(), WatcherID: watcherID, StartedAt: now.Add(-7 * time.Minute), FinishedAt: &finished, Status: "completed", HasAlert: false},
		},
	}

	handler := watcherRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	watcher, ok := resp["watcher"].(map[string]any)
	if !ok {
		t.Fatal("expected watcher field")
	}
	if watcher["title"] != "Connection Watcher" {
		t.Errorf("watcher title = %v, want Connection Watcher", watcher["title"])
	}

	runs, ok := resp["runs"].([]any)
	if !ok {
		t.Fatal("expected runs array")
	}
	if len(runs) != 2 {
		t.Errorf("expected 2 runs, got %d", len(runs))
	}
}

func TestWatcherRunHistoryHandler_InvalidWatcherID(t *testing.T) {
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := watcherRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": "not-a-uuid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid watcher_id")
	}
}

func TestWatcherRunHistoryHandler_WatcherNotFound(t *testing.T) {
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := watcherRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": uuid.New().String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for non-existent watcher")
	}
}

func TestWatcherRunHistoryHandler_EmptyRuns(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Test Watcher", Status: store.WatcherActive},
		},
	}
	rs := &mockWatcherRunStore{}

	handler := watcherRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	runs, ok := resp["runs"].([]any)
	if !ok {
		t.Fatal("expected runs array")
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestWatcherRunHistoryHandler_StatusFilter(t *testing.T) {
	watcherID := uuid.New()
	now := time.Now().UTC()
	finished := now.Add(-1 * time.Minute)

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Test", Status: store.WatcherActive},
		},
	}
	rs := &mockWatcherRunStore{
		runs: []store.WatcherRun{
			{ID: uuid.New(), WatcherID: watcherID, StartedAt: now, FinishedAt: &finished, Status: "completed", HasAlert: false},
			{ID: uuid.New(), WatcherID: watcherID, StartedAt: now, FinishedAt: &finished, Status: "error", HasAlert: false},
			{ID: uuid.New(), WatcherID: watcherID, StartedAt: now, FinishedAt: &finished, Status: "completed", HasAlert: true},
		},
	}

	handler := watcherRunHistoryHandler(ws, rs)

	// Filter for alerted runs only.
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id":    watcherID.String(),
		"status_filter": "alerted",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	runs, ok := resp["runs"].([]any)
	if !ok {
		t.Fatal("expected runs array")
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 alerted run, got %d", len(runs))
	}
}

func TestWatcherRunHistoryHandler_MissingWatcherID(t *testing.T) {
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := watcherRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing watcher_id")
	}
}
